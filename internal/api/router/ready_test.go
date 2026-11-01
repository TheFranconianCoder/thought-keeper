package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestReadOnlyWhileRebuilding: the API serves immediately, but while the index is rebuilt every
// mutation is refused with 503 and a Retry-After. Reads keep working — that is the point of serving
// early at all.
func TestReadOnlyWhileRebuilding(t *testing.T) {
	repairing := false
	r := chi.NewRouter()
	r.Use(readOnlyWhileRebuilding(func() bool { return repairing }))
	r.Get("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Post("/api/v1/reindex", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })

	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
		return rec
	}

	if got := do(http.MethodPost, "/api/v1/reindex"); got.Code != http.StatusAccepted {
		t.Fatalf("write while idle = %d, want 202", got.Code)
	}
	repairing = true
	if got := do(http.MethodGet, "/api/v1/status"); got.Code != http.StatusOK {
		t.Fatalf("read while rebuilding = %d, want 200", got.Code)
	}
	blocked := do(http.MethodPost, "/api/v1/reindex")
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("write while rebuilding = %d, want 503", blocked.Code)
	}
	if blocked.Header().Get("Retry-After") == "" {
		t.Error("a refused write must say when to try again")
	}
	if !strings.Contains(blocked.Body.String(), `"repairing"`) {
		t.Errorf("body = %s, want the repairing code", blocked.Body.String())
	}

	repairing = false
	if got := do(http.MethodPost, "/api/v1/reindex"); got.Code != http.StatusAccepted {
		t.Fatalf("write after the rebuild = %d, want 202", got.Code)
	}
}

// TestReadOnlyWithoutAReadyCheck: a router built by hand (tests, tools) has no readiness source and
// must not refuse anything.
func TestReadOnlyWithoutAReadyCheck(t *testing.T) {
	r := chi.NewRouter()
	r.Use(readOnlyWhileRebuilding(nil))
	r.Post("/api/v1/reindex", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reindex", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("write = %d, want 202", rec.Code)
	}
}
