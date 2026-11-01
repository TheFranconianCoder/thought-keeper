package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

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
	ScheduleListingURLs(parentHash string, urls []string)
	ScheduleListingWalk(parentHash, next string, seed []listing.Item)
}

// SearchHandler serves GET /api/v1/search.
type SearchHandler struct {
	Search usecases.Searcher
}

// Get serves GET /api/v1/search.
func (h SearchHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hits, err := h.Search(r.Context(), q.Get("q"), q.Get("layer"), atoiDefault(q.Get("limit"), 20))
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

// LintHandler serves POST /api/v1/lint and GET /api/v1/lint/findings.
type LintHandler struct {
	Lint     LintScheduler
	Findings usecases.FindingLister
	Mark     MaintenanceMarker
}

// Post schedules a lint run.
func (h LintHandler) Post(w http.ResponseWriter, r *http.Request) {
	var req payloads.LintRequest
	_ = json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck // a malformed value falls back to the zero value
	runID, err := h.Lint.ScheduleLint(req.Scope)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: "running"})
}

// ListFindings serves GET /api/v1/lint/findings.
func (h LintHandler) ListFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	findings, err := h.Findings(r.Context(), q.Get("kind"), atoiDefault(q.Get("limit"), 200), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.FindingList{Findings: payloads.FindingsFromPartials(findings), Count: len(findings)})
}
