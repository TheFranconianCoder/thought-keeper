package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/handlers"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// TestSourceActivityCountsTheBatchAndNamesTheMainSources: the Start statistic answers both halves in
// one read. The count is the batch; `main` is the owner — the feed, not each of its articles.
func TestSourceActivityCountsTheBatchAndNamesTheMainSources(t *testing.T) {
	base := time.Date(2026, 11, 7, 8, 0, 0, 123_000_000, time.UTC)
	h := handlers.SourcesHandler{
		Activity: func(_ context.Context, since time.Time) (usecases.SourceActivity, error) {
			if !since.Equal(base) {
				t.Fatalf("since = %v, want %v", since, base)
			}
			return usecases.SourceActivity{
				Arrived: usecases.SourceActivityGroup{
					Count: 5, Roots: 1,
					Main: []partialentities.PartialSource{
						{Hash: "feed", Kind: "url", Title: "The feed"},
					},
				},
				Changed: usecases.SourceActivityGroup{Count: 2, Roots: 2},
			}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.ListActivity(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sources/activity?since="+url.QueryEscape(base.Format(time.RFC3339Nano)), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		New struct {
			Count int `json:"count"`
			Roots int `json:"roots"`
			Main  []struct {
				Hash  string `json:"hash"`
				Title string `json:"title"`
			} `json:"main"`
		} `json:"new"`
		Changed struct {
			Count int   `json:"count"`
			Roots int   `json:"roots"`
			Main  []any `json:"main"`
		} `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.New.Count != 5 || body.New.Roots != 1 {
		t.Fatalf("new count/roots = %d/%d, want 5/1", body.New.Count, body.New.Roots)
	}
	if len(body.New.Main) != 1 || body.New.Main[0].Title != "The feed" {
		t.Fatalf("new main = %v, want the feed", body.New.Main)
	}
	if body.Changed.Count != 2 || body.Changed.Roots != 2 {
		t.Fatalf("changed count/roots = %d/%d, want 2/2", body.Changed.Count, body.Changed.Roots)
	}
	if body.Changed.Main == nil {
		t.Fatal("changed main = null, want an empty list")
	}
}

// TestSourceActivityNeedsItsInstant keeps a missing or broken baseline from answering "everything".
func TestSourceActivityNeedsItsInstant(t *testing.T) {
	h := handlers.SourcesHandler{
		Activity: func(context.Context, time.Time) (usecases.SourceActivity, error) {
			t.Fatal("the statistic ran without a baseline")
			return usecases.SourceActivity{}, nil
		},
	}
	for _, target := range []string{
		"/api/v1/sources/activity",
		"/api/v1/sources/activity?since=yesterday",
	} {
		rec := httptest.NewRecorder()
		h.ListActivity(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}
