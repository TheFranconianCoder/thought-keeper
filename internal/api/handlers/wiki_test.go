package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

type staticWikiSaver struct{ res usecases.WikiPageResult }

func (s staticWikiSaver) Execute(context.Context, usecases.WikiPageInput) (usecases.WikiPageResult, error) {
	return s.res, nil
}

// TestWikiSaveSchedulesCleanupInsteadOfDeleting: a save hands the page to the app and returns. The
// pruning is the app's background job; the request must not wait for a delete (a delete used to walk
// the whole atom vault — 73 seconds for three links).
func TestWikiSaveSchedulesCleanupInsteadOfDeleting(t *testing.T) {
	id := ulid.Make()
	saver := staticWikiSaver{res: usecases.WikiPageResult{
		Page: entities.WikiPage{ID: id, Title: "Page", Body: "body"},
		Hash: entities.WikiHash(id), Created: true, StaleSourceHash: "stale",
	}}
	var gotPage, gotStale string
	h := WikiHandler{
		Save: saver,
		OnCleanup: func(pageID, staleHash string) {
			gotPage, gotStale = pageID, staleHash
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/wiki",
		strings.NewReader(`{"title":"Page","tags":[],"body":"body"}`))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotPage != id.String() || gotStale != "stale" {
		t.Fatalf("cleanup = (%q, %q), want (%q, %q)", gotPage, gotStale, id, "stale")
	}
}

// TestWikiSaveAlwaysSchedulesTheConvergencePass: the pass runs after EVERY save. It re-reads the page
// and is a no-op when the page and its link sources agree — which is what heals a save whose cleanup
// was lost (a crash between the page file and the job) instead of leaving an orphan behind forever.
func TestWikiSaveAlwaysSchedulesTheConvergencePass(t *testing.T) {
	id := ulid.Make()
	saver := staticWikiSaver{res: usecases.WikiPageResult{
		Page: entities.WikiPage{ID: id, Title: "Page", Body: "body"},
		Hash: entities.WikiHash(id), Created: true,
	}}
	var gotPage, gotStale string
	h := WikiHandler{
		Save: saver,
		OnCleanup: func(pageID, staleHash string) {
			gotPage, gotStale = pageID, staleHash
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/wiki",
		strings.NewReader(`{"title":"Page","tags":[],"body":"body"}`))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotPage != id.String() || gotStale != "" {
		t.Fatalf("cleanup = (%q, %q), want (%q, \"\")", gotPage, gotStale, id)
	}
}
