package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// URLRefresher re-fetches the known URLs. Execute respects the configured cadence (a scheduled
// pass); ExecuteAll ignores it (the manual pass behind the button); ExecuteOne refreshes a single
// source now (the per-source Refresh button).
type URLRefresher interface {
	Execute(ctx context.Context) (usecases.RefreshResult, error)
	ExecuteAll(ctx context.Context) (usecases.RefreshResult, error)
	ExecuteOne(ctx context.Context, hash string) (usecases.RefreshResult, error)
}

// URLHandler serves the URL maintenance endpoints.
type URLHandler struct {
	Refresh URLRefresher
	Bulk    URLBulkScheduler
	// OnChanged is called for every URL whose content changed, so the app can
	// build trees, schedule the document steps and emit an SSE event.
	OnChanged func(hash string)
	// OnDiscovered hands the NEW pages a feed or a sitemap declared to the fetch queue. Claiming them
	// without fetching would list sources that never arrive.
	OnDiscovered func(children []usecases.DiscoveredChildren)
	// Addresses is the cleanup task that moves URL sources onto their URL's address.
	Addresses URLAddressMigrator
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

// URLAddressMigrator moves URL sources onto their URL's address (the cleanup task for a vault written
// when a URL source was addressed by its first snapshot's hash).
type URLAddressMigrator interface {
	Execute(ctx context.Context, apply bool) (usecases.MigrateURLAddressesResult, error)
}

// GetAddresses serves GET /api/v1/urls/addresses: the read-only preview, so the button can say what it
// would do before it is pressed.
func (h URLHandler) GetAddresses(w http.ResponseWriter, r *http.Request) {
	if h.Addresses == nil {
		payloads.WriteError(w, types.New("not_implemented", "url addresses are not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.Addresses.Execute(r.Context(), false)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.URLAddressesResult{
		Checked: res.Checked, Pending: res.Pending, Count: len(res.Pending),
	})
}

// PostMigrate serves POST /api/v1/urls/migrate: move every URL source onto its URL's address. It is a
// rename, not a re-derivation — the claims, their provenance and the derived files all come along.
func (h URLHandler) PostMigrate(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("migrate-urls", "")()
	}
	if h.Addresses == nil {
		payloads.WriteError(w, types.New("not_implemented", "url addresses are not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.Addresses.Execute(r.Context(), true)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnChanged != nil {
		// Every source moved: the list is different now (its rows have other addresses).
		h.OnChanged("")
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.URLMigrateResult{
		Checked: res.Checked, Moved: res.Moved, Atoms: res.Atoms, Skipped: res.Skipped,
		Pending: res.Pending,
	})
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
	res, err := h.Refresh.ExecuteAll(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnChanged != nil {
		for _, s := range res.Sources {
			h.OnChanged(s.Hash)
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
	res, err := h.Refresh.ExecuteOne(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnChanged != nil {
		for _, s := range res.Sources {
			h.OnChanged(s.Hash)
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
