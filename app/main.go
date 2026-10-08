// StatusBoard: a public status page and incident tracker.
//
// Configuration (environment variables):
//
//	PORT              public HTTP port (default 8080)
//	METRICS_PORT      Prometheus /metrics port, never exposed publicly (default 9090)
//	DATABASE_URL      postgres://user:pass@host:5432/db  (empty = in-memory store, for demos only)
//	REDIS_ADDR        host:6379 of Redis or Valkey        (empty = no cache)
//	REDIS_PASSWORD    optional
//	ADMIN_TOKEN       bearer token for the write API      (empty = write API disabled)
//	SITE_NAME         shown in the page header (default "StatusBoard")
//	ENVIRONMENT       staging / production (anything except "production" shows a banner)
//	SEED_COMPONENTS   comma-separated components created when the database is empty
//	CACHE_TTL         how long the summary stays cached (default 30s)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/statusboard/statusboard/internal/cache"
	"github.com/statusboard/statusboard/internal/store"
	"github.com/statusboard/statusboard/internal/web"
)

// version is set at build time: -ldflags "-X main.version=<git sha>"
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "statusboard", "version", version)
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	environment := env("ENVIRONMENT", "development")
	ttl, err := time.ParseDuration(env("CACHE_TTL", "30s"))
	if err != nil {
		return err
	}

	// ---- storage ----
	var st store.Store
	if url := os.Getenv("DATABASE_URL"); url != "" {
		startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		pg, err := connectWithRetry(startCtx, log, url)
		cancel()
		if err != nil {
			return err
		}
		st = pg
	} else {
		log.Warn("DATABASE_URL is empty: using the in-memory store (data is lost on restart)")
		st = store.NewMemory()
	}
	defer st.Close()

	if seed := os.Getenv("SEED_COMPONENTS"); seed != "" {
		if err := store.Seed(ctx, st, strings.Split(seed, ",")); err != nil {
			return err
		}
	}

	// ---- cache ----
	var c cache.Cache = cache.None{}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		c = cache.NewRedis(addr, os.Getenv("REDIS_PASSWORD"))
	}

	if os.Getenv("ADMIN_TOKEN") == "" {
		log.Warn("ADMIN_TOKEN is empty: the write API is disabled")
	}

	metrics := web.NewMetrics(version, environment)
	srv := web.New(web.Config{
		SiteName:    env("SITE_NAME", "StatusBoard"),
		Environment: environment,
		Version:     version,
		AdminToken:  os.Getenv("ADMIN_TOKEN"),
		CacheTTL:    ttl,
	}, st, c, metrics, log)

	public := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}

	// Metrics on their own port so the Ingress never exposes them to the internet
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
	internal := &http.Server{Addr: ":" + env("METRICS_PORT", "9090"), Handler: metricsMux, ReadHeaderTimeout: 5 * time.Second}

	errs := make(chan error, 2)
	for _, s := range []*http.Server{public, internal} {
		go func(s *http.Server) {
			log.Info("listening", "addr", s.Addr, "environment", environment)
			if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}(s)
	}

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	// Graceful shutdown: Kubernetes sends SIGTERM, then waits terminationGracePeriodSeconds.
	// Finish in-flight requests instead of cutting them off.
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = internal.Shutdown(shutdownCtx)
	return public.Shutdown(shutdownCtx)
}

// connectWithRetry keeps trying while PostgreSQL starts (common on first deploy).
func connectWithRetry(ctx context.Context, log *slog.Logger, url string) (*store.Postgres, error) {
	for attempt := 1; ; attempt++ {
		pg, err := store.NewPostgres(ctx, url)
		if err == nil {
			log.Info("connected to PostgreSQL", "attempt", attempt)
			return pg, nil
		}
		log.Warn("PostgreSQL not ready, retrying", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(3 * time.Second):
		}
	}
}
