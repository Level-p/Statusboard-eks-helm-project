package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/statusboard/statusboard/internal/cache"
	"github.com/statusboard/statusboard/internal/store"
)

const token = "test-token"

// fakeCache is an in-memory cache that can be switched to "broken".
type fakeCache struct {
	data   map[string][]byte
	broken bool
}

func (f *fakeCache) Get(_ context.Context, k string) ([]byte, error) {
	if f.broken {
		return nil, errors.New("connection refused")
	}
	if v, ok := f.data[k]; ok {
		return v, nil
	}
	return nil, cache.ErrMiss
}
func (f *fakeCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	if f.broken {
		return errors.New("connection refused")
	}
	f.data[k] = v
	return nil
}
func (f *fakeCache) Delete(_ context.Context, k string) error { delete(f.data, k); return nil }
func (f *fakeCache) Ping(context.Context) error {
	if f.broken {
		return errors.New("connection refused")
	}
	return nil
}

func setup(t *testing.T) (*Server, http.Handler, *store.Memory, *fakeCache) {
	t.Helper()
	mem := store.NewMemory()
	fc := &fakeCache{data: map[string][]byte{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Config{SiteName: "Test", Environment: "staging", Version: "t", AdminToken: token}, mem, fc, NewMetrics("t", "staging"), log)
	return s, s.Handler(), mem, fc
}

func do(t *testing.T, h http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestStatusPageRendersWithBanner(t *testing.T) {
	_, h, mem, _ := setup(t)
	_, _ = mem.CreateComponent(context.Background(), "Website", "")
	rec := do(t, h, "GET", "/", "", false)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"All systems operational", "Website", "staging environment"} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

func TestWriteAPIRequiresToken(t *testing.T) {
	_, h, _, _ := setup(t)
	if rec := do(t, h, "POST", "/api/v1/components", `{"name":"API"}`, false); rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/components", `{"name":"API"}`, true); rec.Code != 201 {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body)
	}
}

func TestComponentStatusChangesOverallAndInvalidatesCache(t *testing.T) {
	_, h, _, fc := setup(t)
	c := decode[store.Component](t, do(t, h, "POST", "/api/v1/components", `{"name":"Checkout"}`, true))

	// First read fills the cache
	if got := decode[Summary](t, do(t, h, "GET", "/api/v1/status", "", false)); got.Overall != "operational" {
		t.Fatalf("overall = %s", got.Overall)
	}
	if _, ok := fc.data[summaryKey]; !ok {
		t.Fatal("summary was not cached")
	}

	rec := do(t, h, "PATCH", "/api/v1/components/"+itoa(c.ID), `{"status":"partial_outage"}`, true)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if _, ok := fc.data[summaryKey]; ok {
		t.Fatal("cache was not invalidated after a change")
	}
	if got := decode[Summary](t, do(t, h, "GET", "/api/v1/status", "", false)); got.Overall != "partial_outage" {
		t.Fatalf("overall = %s, want partial_outage", got.Overall)
	}
}

func TestIncidentLifecycle(t *testing.T) {
	s, h, _, _ := setup(t)
	in := decode[store.Incident](t, do(t, h, "POST", "/api/v1/incidents",
		`{"title":"Checkout errors","impact":"critical","message":"We are investigating."}`, true))
	if in.Status != "investigating" {
		t.Fatalf("status = %s", in.Status)
	}
	sum := decode[Summary](t, do(t, h, "GET", "/api/v1/status", "", false))
	if sum.Overall != "major_outage" || len(sum.ActiveIncidents) != 1 {
		t.Fatalf("critical incident not reflected: %+v", sum)
	}
	if v := testutil.ToFloat64(s.metrics.openIncidents); v != 1 {
		t.Fatalf("open incidents metric = %v", v)
	}

	in = decode[store.Incident](t, do(t, h, "POST", "/api/v1/incidents/"+itoa(in.ID)+"/updates",
		`{"status":"resolved","message":"Fixed by rolling back."}`, true))
	if in.ResolvedAt == nil || len(in.Updates) != 2 || in.Updates[0].Status != "resolved" {
		t.Fatalf("bad resolved incident: %+v", in)
	}
	sum = decode[Summary](t, do(t, h, "GET", "/api/v1/status", "", false))
	if sum.Overall != "operational" || len(sum.RecentIncidents) != 1 {
		t.Fatalf("resolved incident not reflected: %+v", sum)
	}
	if rec := do(t, h, "GET", "/incidents/"+itoa(in.ID), "", false); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Fixed by rolling back.") {
		t.Fatalf("incident page: %d", rec.Code)
	}
}

func TestValidationAndNotFound(t *testing.T) {
	_, h, _, _ := setup(t)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/v1/incidents", `{"title":"x","impact":"huge","message":"m"}`, 400},
		{"POST", "/api/v1/incidents", `not json`, 400},
		{"PATCH", "/api/v1/components/99", `{"status":"operational"}`, 404},
		{"PATCH", "/api/v1/components/abc", `{"status":"operational"}`, 400},
		{"GET", "/api/v1/incidents/42", ``, 404},
	}
	for _, c := range cases {
		if rec := do(t, h, c.method, c.path, c.body, true); rec.Code != c.want {
			t.Errorf("%s %s: got %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestReadinessDependsOnDatabaseNotCache(t *testing.T) {
	_, h, mem, fc := setup(t)

	fc.broken = true
	if rec := do(t, h, "GET", "/readyz", "", false); rec.Code != 200 {
		t.Fatalf("cache outage should not make the pod unready, got %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/", "", false); rec.Code != 200 {
		t.Fatalf("page should still work from the database, got %d", rec.Code)
	}

	mem.PingErr = errors.New("database down")
	if rec := do(t, h, "GET", "/readyz", "", false); rec.Code != 503 {
		t.Fatalf("database outage should make the pod unready, got %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/healthz", "", false); rec.Code != 200 {
		t.Fatalf("liveness must not depend on the database, got %d", rec.Code)
	}
}

func TestRequestMetricsUseRoutePattern(t *testing.T) {
	s, h, _, _ := setup(t)
	do(t, h, "GET", "/api/v1/incidents/7", "", false)
	got := testutil.ToFloat64(s.metrics.requests.WithLabelValues("GET", "GET /api/v1/incidents/{id}", "404"))
	if got != 1 {
		t.Fatalf("expected one request recorded under the route pattern, got %v", got)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
