// Package web serves the public status page, the JSON API and the health checks.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/statusboard/statusboard/internal/cache"
	"github.com/statusboard/statusboard/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

const summaryKey = "statusboard:summary"

// Summary is what the status page shows. It is cached as JSON.
type Summary struct {
	Overall         string            `json:"overall"`
	Components      []store.Component `json:"components"`
	ActiveIncidents []store.Incident  `json:"active_incidents"`
	RecentIncidents []store.Incident  `json:"recent_incidents"`
	GeneratedAt     time.Time         `json:"generated_at"`
}

type Config struct {
	SiteName    string
	Environment string // "production" hides the environment banner
	Version     string
	AdminToken  string
	CacheTTL    time.Duration
}

type Server struct {
	cfg     Config
	store   store.Store
	cache   cache.Cache
	metrics *Metrics
	log     *slog.Logger
	tmpl    *template.Template
}

func New(cfg Config, s store.Store, c cache.Cache, m *Metrics, log *slog.Logger) *Server {
	if c == nil {
		c = cache.None{}
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 30 * time.Second
	}
	funcs := template.FuncMap{
		"label": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
		"when":  func(t time.Time) string { return t.UTC().Format("02 Jan 2006, 15:04 UTC") },
	}
	tmpl := template.Must(template.New("").Funcs(funcs).ParseFS(assets, "templates/*.html"))
	return &Server{cfg: cfg, store: s, cache: c, metrics: m, log: log, tmpl: tmpl}
}

// Handler returns the public router (status page, API, health checks), wrapped with metrics.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /{$}", s.statusPage)
	mux.HandleFunc("GET /incidents/{id}", s.incidentPage)

	mux.HandleFunc("GET /api/v1/status", s.apiStatus)
	mux.HandleFunc("GET /api/v1/components", s.apiListComponents)
	mux.HandleFunc("POST /api/v1/components", s.admin(s.apiCreateComponent))
	mux.HandleFunc("PATCH /api/v1/components/{id}", s.admin(s.apiSetComponentStatus))
	mux.HandleFunc("GET /api/v1/incidents", s.apiListIncidents)
	mux.HandleFunc("GET /api/v1/incidents/{id}", s.apiGetIncident)
	mux.HandleFunc("POST /api/v1/incidents", s.admin(s.apiCreateIncident))
	mux.HandleFunc("POST /api/v1/incidents/{id}/updates", s.admin(s.apiAddIncidentUpdate))

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	return s.metrics.Instrument(mux)
}

// ---------- summary with cache-aside ----------

func (s *Server) summary(ctx context.Context) (Summary, error) {
	if b, err := s.cache.Get(ctx, summaryKey); err == nil {
		var sum Summary
		if json.Unmarshal(b, &sum) == nil {
			s.metrics.cache.WithLabelValues("hit").Inc()
			return sum, nil
		}
	} else if errors.Is(err, cache.ErrMiss) {
		s.metrics.cache.WithLabelValues("miss").Inc()
	} else {
		s.metrics.cache.WithLabelValues("error").Inc()
		s.log.Warn("cache read failed, using the database", "error", err)
	}

	sum, err := s.buildSummary(ctx)
	if err != nil {
		return Summary{}, err
	}
	if b, err := json.Marshal(sum); err == nil {
		if err := s.cache.Set(ctx, summaryKey, b, s.cfg.CacheTTL); err != nil {
			s.log.Warn("cache write failed", "error", err)
		}
	}
	return sum, nil
}

func (s *Server) buildSummary(ctx context.Context) (Summary, error) {
	comps, err := s.store.ListComponents(ctx)
	if err != nil {
		return Summary{}, err
	}
	all, err := s.store.ListIncidents(ctx, true, 20)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Overall: store.StatusOperational, Components: comps, GeneratedAt: time.Now().UTC(),
		ActiveIncidents: []store.Incident{}, RecentIncidents: []store.Incident{}}
	counts := map[string]float64{store.StatusOperational: 0, store.StatusDegraded: 0, store.StatusPartialOutage: 0, store.StatusMajorOutage: 0}
	for _, c := range comps {
		sum.Overall = store.Worst(sum.Overall, c.Status)
		counts[c.Status]++
	}
	for _, in := range all {
		if in.Status == store.IncidentResolved {
			if len(sum.RecentIncidents) < 5 {
				sum.RecentIncidents = append(sum.RecentIncidents, in)
			}
			continue
		}
		sum.ActiveIncidents = append(sum.ActiveIncidents, in)
		if in.Impact == store.ImpactCritical {
			sum.Overall = store.Worst(sum.Overall, store.StatusMajorOutage)
		} else {
			sum.Overall = store.Worst(sum.Overall, store.StatusDegraded)
		}
	}
	s.metrics.openIncidents.Set(float64(len(sum.ActiveIncidents)))
	for status, n := range counts {
		s.metrics.components.WithLabelValues(status).Set(n)
	}
	return sum, nil
}

// invalidate drops the cached summary after any change, so the page is never stale.
func (s *Server) invalidate(ctx context.Context) {
	if err := s.cache.Delete(ctx, summaryKey); err != nil {
		s.log.Warn("cache invalidation failed; the page may be stale until the TTL expires", "error", err)
	}
}

// ---------- HTML pages ----------

type pageData struct {
	SiteName    string
	Environment string
	ShowBanner  bool
	Version     string
	Summary     Summary
	Incident    store.Incident
}

func (s *Server) page() pageData {
	return pageData{SiteName: s.cfg.SiteName, Environment: s.cfg.Environment, Version: s.cfg.Version,
		ShowBanner: s.cfg.Environment != "production"}
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	sum, err := s.summary(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := s.page()
	d.Summary = sum
	s.render(w, "status.html", d)
}

func (s *Server) incidentPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	in, err := s.store.GetIncident(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := s.page()
	d.Incident = in
	s.render(w, "incident.html", d)
}

func (s *Server) render(w http.ResponseWriter, name string, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, d); err != nil {
		s.log.Error("template failed", "template", name, "error", err)
	}
}

// ---------- JSON API ----------

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	sum, err := s.summary(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) apiListComponents(w http.ResponseWriter, r *http.Request) {
	comps, err := s.store.ListComponents(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, comps)
}

func (s *Server) apiCreateComponent(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Description string }
	if !readJSON(w, r, &in) {
		return
	}
	c, err := s.store.CreateComponent(r.Context(), in.Name, in.Description)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidate(r.Context())
	s.log.Info("component created", "id", c.ID, "name", c.Name)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) apiSetComponentStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct{ Status string }
	if !readJSON(w, r, &in) {
		return
	}
	c, err := s.store.SetComponentStatus(r.Context(), id, in.Status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidate(r.Context())
	s.log.Info("component status changed", "id", c.ID, "name", c.Name, "status", c.Status)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) apiListIncidents(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	list, err := s.store.ListIncidents(r.Context(), all, 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []store.Incident{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) apiGetIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	in, err := s.store.GetIncident(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) apiCreateIncident(w http.ResponseWriter, r *http.Request) {
	var in struct{ Title, Impact, Message string }
	if !readJSON(w, r, &in) {
		return
	}
	inc, err := s.store.CreateIncident(r.Context(), in.Title, in.Impact, in.Message)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidate(r.Context())
	s.log.Info("incident opened", "id", inc.ID, "impact", inc.Impact, "title", inc.Title)
	writeJSON(w, http.StatusCreated, inc)
}

func (s *Server) apiAddIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct{ Status, Message string }
	if !readJSON(w, r, &in) {
		return
	}
	inc, err := s.store.AddIncidentUpdate(r.Context(), id, in.Status, in.Message)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidate(r.Context())
	s.log.Info("incident updated", "id", inc.ID, "status", inc.Status)
	writeJSON(w, http.StatusCreated, inc)
}

// ---------- health checks ----------

// healthz (liveness): the process is up and can serve HTTP. It never checks
// dependencies, otherwise a database outage would make Kubernetes restart every pod.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz (readiness): the pod can do useful work, so it needs the database.
// The cache is optional: if it is down the pod stays ready and reads from PostgreSQL.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	res := map[string]string{"database": "ok", "cache": "ok"}
	code := http.StatusOK
	if err := s.store.Ping(ctx); err != nil {
		res["database"] = err.Error()
		code = http.StatusServiceUnavailable
	}
	if err := s.cache.Ping(ctx); err != nil {
		res["cache"] = "degraded: " + err.Error()
	}
	writeJSON(w, code, res)
}

// ---------- helpers ----------

// admin protects write endpoints with a bearer token (constant-time comparison).
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.cfg.AdminToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong admin token"})
			return
		}
		next(w, r)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, store.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid input"})
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a positive number"})
		return 0, false
	}
	return id, true
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be valid JSON"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
