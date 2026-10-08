package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics are the application's own Prometheus metrics (RED method plus business metrics).
type Metrics struct {
	Registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	cache         *prometheus.CounterVec
	openIncidents prometheus.Gauge
	components    *prometheus.GaugeVec
}

func NewMetrics(version, environment string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "statusboard_http_requests_total",
			Help: "HTTP requests by method, route and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "statusboard_http_request_duration_seconds",
			Help:    "HTTP request duration by method and route.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"method", "route"}),
		cache: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "statusboard_cache_requests_total",
			Help: "Summary cache lookups by result (hit, miss, error).",
		}, []string{"result"}),
		openIncidents: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "statusboard_open_incidents",
			Help: "Incidents that are not resolved yet.",
		}),
		components: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "statusboard_components",
			Help: "Number of components in each status.",
		}, []string{"status"}),
	}
	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "statusboard_build_info",
		Help:        "Always 1; labels show the running version and environment.",
		ConstLabels: prometheus.Labels{"version": version, "environment": environment},
	})
	build.Set(1)
	reg.MustRegister(m.requests, m.duration, m.cache, m.openIncidents, m.components, build,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) { s.code = code; s.ResponseWriter.WriteHeader(code) }

// Instrument records count and duration for every request. The route label is
// the matched pattern (for example "GET /api/v1/incidents/{id}"), never the raw
// path, so the number of time series stays small.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.code)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
