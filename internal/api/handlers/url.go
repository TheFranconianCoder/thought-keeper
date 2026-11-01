package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// URLHandler serves the URL maintenance endpoints.
type URLHandler struct {
	Refresh usecases.URLRefresher
	// RefreshAll, RefreshOne and RefreshOneForce are the manual passes beside the cadence.
	RefreshAll      usecases.URLRefresherAll
	RefreshOne      usecases.URLRefresherOne
	RefreshOneForce usecases.URLRefresherOneForce
	Bulk            URLBulkScheduler
	// OnChanged is called for every URL whose content changed, so the app can
	// build trees, schedule the document steps and emit an SSE event.
	OnChanged func(hash string)
	// OnReplaced is called for a source whose snapshot changed by DIFFERENT text. The app resets the
	// old derivation exclusively and derives again; the handler must not also call OnChanged for it.
	OnReplaced func(hash, reason string)
	// OnDiscovered hands the NEW pages a feed or a sitemap declared to the fetch queue. Claiming them
	// without fetching would list sources that never arrive.
	OnDiscovered func(children []usecases.DiscoveredChildren)
	// Mark shows a synchronous pass in the Running list while it works.
	Mark MaintenanceMarker
}

// PostMany serves POST /api/v1/add-urls: queue a list of URLs (one per line, e.g.
// a bookmarks export) for background intake.
func (h URLHandler) PostMany(w http.ResponseWriter, r *http.Request) {
	if h.Bulk == nil {
		payloads.WriteError(w, types.New("not_implemented", "url intake is not configured", types.ErrNotImplemented))
		return
	}
	var req payloads.AddURLsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	accepted, err := h.Bulk.ScheduleURLs(req.URLs)
	if err != nil && accepted == 0 {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.AddURLsResult{Accepted: accepted})
}

// discoveredCount counts the new children across the owners of one pass.
func discoveredCount(discovered []usecases.DiscoveredChildren) int {
	n := 0
	for _, d := range discovered {
		n += len(d.URLs)
	}
	return n
}

// Post serves POST /api/v1/refresh-urls: one synchronous refresh pass.
func (h URLHandler) Post(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("refresh-urls", "")()
	}
	if h.Refresh == nil {
		payloads.WriteError(w, types.New("not_implemented", "url refresh is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.RefreshAll(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	resets := make(map[string]bool, len(res.Resets))
	for _, r := range res.Resets {
		resets[r.Hash] = true
		if h.OnReplaced != nil {
			h.OnReplaced(r.Hash, r.Reason)
		}
	}
	if h.OnChanged != nil {
		for _, s := range res.Sources {
			if !resets[s.Hash] {
				h.OnChanged(s.Hash)
			}
		}
	}
	if h.OnDiscovered != nil && len(res.Discovered) > 0 {
		h.OnDiscovered(res.Discovered)
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.RefreshResult{
		Checked:    res.Checked,
		Changed:    res.Changed,
		Failed:     res.Failed,
		Blocked:    res.Blocked,
		Attached:   res.Attached,
		Discovered: discoveredCount(res.Discovered),
		Removed:    res.Removed,
	})
}

// PostOne serves POST /api/v1/sources/{hash}/refresh: ONE URL source, now, whatever its age. It is
// the Refresh button on a source page — a feed or a sitemap re-expands, a page is re-fetched.
func (h URLHandler) PostOne(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("refresh-source", hash)()
	}
	if h.Refresh == nil {
		payloads.WriteError(w, types.New("not_implemented", "url refresh is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.RefreshOne(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	resets := make(map[string]bool, len(res.Resets))
	for _, r := range res.Resets {
		resets[r.Hash] = true
		if h.OnReplaced != nil {
			h.OnReplaced(r.Hash, r.Reason)
		}
	}
	if h.OnChanged != nil {
		for _, s := range res.Sources {
			if !resets[s.Hash] {
				h.OnChanged(s.Hash)
			}
		}
	}
	if h.OnDiscovered != nil && len(res.Discovered) > 0 {
		h.OnDiscovered(res.Discovered)
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.RefreshResult{
		Checked:    res.Checked,
		Changed:    res.Changed,
		Failed:     res.Failed,
		Blocked:    res.Blocked,
		Attached:   res.Attached,
		Discovered: discoveredCount(res.Discovered),
		Removed:    res.Removed,
		Detail:     res.LastError,
	})
}

// Refetch serves POST /api/v1/sources/{hash}/refetch: try the URL again even when its snapshot was
// attached by hand. A readable page replaces the attachment and clears the marker; a wall or a
// failure keeps the human's snapshot exactly as it was. The explicit counterpart to the automatic
// refresh, which never fetches an attached source.
func (h URLHandler) Refetch(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("refetch-url", hash)()
	}
	if h.Refresh == nil {
		payloads.WriteError(w, types.New("not_implemented", "url refetch is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.RefreshOneForce(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnChanged != nil {
		h.OnChanged(hash)
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.RefetchResult{
		Replaced: res.Replaced > 0 || res.Changed > 0,
		Blocked:  res.Blocked > 0,
		Failed:   res.Failed > 0,
		Detail:   res.LastError,
	})
}
