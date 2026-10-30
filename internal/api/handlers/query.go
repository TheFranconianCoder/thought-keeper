package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Searcher runs a lexical search.
type Searcher interface {
	Execute(ctx context.Context, query, layer string, limit int) ([]partialentities.PartialHit, error)
}

// QueryScheduler schedules a query run. A non-empty source scopes it to that one source.
type QueryScheduler interface {
	ScheduleQuery(question, source string) (string, error)
}

// LintScheduler schedules a lint run.
type LintScheduler interface {
	ScheduleLint(scope string) (string, error)
}

// URLBulkScheduler accepts URLs for background intake: a batch from a bookmarks file, and the links a
// wiki page declared. Neither fetches behind the request — the caller gets an accepted count (or
// nothing) and the queue does the work.
type URLBulkScheduler interface {
	ScheduleURLs(urls []string) (int, error)
	ScheduleWikiURLs(pageID string, urls []string)
	ScheduleSitemapURLs(parentHash string, urls []string)
	ScheduleFeedURLs(parentHash string, urls []string)
}

// FindingLister lists lint findings.
type FindingLister interface {
	Execute(ctx context.Context, kind string, limit, offset int) ([]partialentities.PartialFinding, error)
}

// SearchHandler serves GET /api/v1/search.
type SearchHandler struct {
	Search Searcher
}

// Get serves GET /api/v1/search.
func (h SearchHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hits, err := h.Search.Execute(r.Context(), q.Get("q"), q.Get("layer"), atoiDefault(q.Get("limit"), 20))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.SearchResult{Hits: payloads.HitsFromPartials(hits), Count: len(hits)})
}

// QueryHandler serves POST /api/v1/query.
type QueryHandler struct {
	Query QueryScheduler
}

// Post schedules a query run.
func (h QueryHandler) Post(w http.ResponseWriter, r *http.Request) {
	var req payloads.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	if len([]rune(req.Question)) < 3 {
		payloads.WriteError(w, types.New("invalid_body", "question is required", types.ErrInvalidURL))
		return
	}
	source := strings.TrimSpace(req.Source)
	if source != "" && !entities.IsSHA256Hex(source) {
		payloads.WriteError(w, types.New("invalid_hash", "source must be a SHA-256 hash", types.ErrInvalidHash))
		return
	}
	runID, err := h.Query.ScheduleQuery(req.Question, source)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: "running"})
}

// Cleaning repairs unattributable pages and missing tags.
type Cleaning interface {
	Execute(ctx context.Context) (usecases.CleanupResult, error)
}

// LintHandler serves POST /api/v1/lint and GET /api/v1/lint/findings.
type LintHandler struct {
	Lint     LintScheduler
	Findings FindingLister
	Mark     MaintenanceMarker
	Cleanup  Cleaning
}

// CleanupPost serves POST /api/v1/cleanup.
func (h LintHandler) CleanupPost(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("cleanup", "")()
	}
	if h.Cleanup == nil {
		payloads.WriteError(w, types.New("not_implemented", "cleanup is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.Cleanup.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.CleanupResult{
		ClustersPruned:  res.ClustersPruned,
		ClustersRemoved: res.ClustersRemoved,
		Findings:        res.Findings,
	})
}

// Post schedules a lint run.
func (h LintHandler) Post(w http.ResponseWriter, r *http.Request) {
	var req payloads.LintRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	runID, err := h.Lint.ScheduleLint(req.Scope)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: "running"})
}

// Findings serves GET /api/v1/lint/findings.
func (h LintHandler) ListFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	findings, err := h.Findings.Execute(r.Context(), q.Get("kind"), atoiDefault(q.Get("limit"), 200), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.FindingList{Findings: payloads.FindingsFromPartials(findings), Count: len(findings)})
}
