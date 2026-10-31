package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// WikiLister lists pages and the tag cloud.
type WikiLister interface {
	Execute(ctx context.Context) (usecases.WikiList, error)
}

// WikiGetter reads one page.
type WikiGetter interface {
	Execute(ctx context.Context, id ulid.ULID) (entities.WikiPage, error)
}

// WikiSaver writes a page and its source representation.
type WikiSaver interface {
	Execute(ctx context.Context, in usecases.WikiPageInput) (usecases.WikiPageResult, error)
}

// WikiDeleter removes a page and its source representation.
type WikiDeleter interface {
	Execute(ctx context.Context, id ulid.ULID) (string, []string, error)
}

// WikiFavoriter writes the reader's star on a page.
type WikiFavoriter interface {
	Execute(ctx context.Context, id ulid.ULID, on bool) (entities.WikiPage, error)
}

// WikiURLScheduler queues the links a page declared that no source knows yet. The fetch happens in the
// background — the save is over when the page is on disk — and the link is a source row from that
// moment on: pending, then green, or red with the reason.
type WikiURLScheduler interface {
	ScheduleWikiURLs(pageID string, urls []string)
}

// WikiHandler serves the wiki endpoints.
type WikiHandler struct {
	List     WikiLister
	Get      WikiGetter
	Save     WikiSaver
	Delete   WikiDeleter
	Favorite WikiFavoriter
	// OnChanged is called after a save, so the app can schedule the source and notify other windows.
	OnChanged func(id, hash string)
	// OnReprocess clears the old derivation of a changed page and only then pumps the source. It is
	// a background job: a save returns as soon as the page and its new source representation are on
	// disk.
	OnReprocess func(hash string)
	// OnTag asks the app to derive the page's tags from its own title and prose. It is a background
	// run: a save must never wait for a model call.
	OnTag func(pageID string)
	// OnDeleted is called with the removed source hash, so the app can pump what is left.
	OnDeleted func(hash string)
	// URLs schedules the page's NEW links; the app fetches them off the request path. OnURLRemoved
	// clean-up runs for a URL source a deleted page stopped owning.
	URLs         WikiURLScheduler
	OnURLRemoved func(hash string)
	// OnCleanup asks the app to prune what the saved page stopped owning: the link sources it no
	// longer declares and an older representation it pointed at. It runs after EVERY save — the pass
	// re-reads the page, so it is a no-op when nothing changed and heals a save whose cleanup was
	// lost. It is a background job: a save deletes nothing, and the page file is already the truth.
	OnCleanup func(pageID, staleHash string)
}

// PutFavorite serves PUT /api/v1/wiki/{id}/favorite.
func (h WikiHandler) PutFavorite(w http.ResponseWriter, r *http.Request) { h.favorite(w, r, true) }

// DeleteFavorite serves DELETE /api/v1/wiki/{id}/favorite.
func (h WikiHandler) DeleteFavorite(w http.ResponseWriter, r *http.Request) { h.favorite(w, r, false) }

func (h WikiHandler) favorite(w http.ResponseWriter, r *http.Request, on bool) {
	id, ok := wikiID(w, r)
	if !ok {
		return
	}
	if h.Favorite == nil {
		payloads.WriteError(w, types.New("not_implemented", "the favorite is not available", types.ErrNotImplemented))
		return
	}
	page, err := h.Favorite.Execute(r.Context(), id, on)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, wikiPagePayload(page))
}

// List serves GET /api/v1/wiki.
func (h WikiHandler) ListPages(w http.ResponseWriter, r *http.Request) {
	res, err := h.List.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.WikiListFromUseCase(res))
}

// Get serves GET /api/v1/wiki/{id}.
func (h WikiHandler) GetPage(w http.ResponseWriter, r *http.Request) {
	id, ok := wikiID(w, r)
	if !ok {
		return
	}
	page, err := h.Get.Execute(r.Context(), id)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, wikiPagePayload(page))
}

// Create serves POST /api/v1/wiki.
func (h WikiHandler) Create(w http.ResponseWriter, r *http.Request) {
	h.save(w, r, ulid.ULID{})
}

// Update serves PUT /api/v1/wiki/{id}.
func (h WikiHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := wikiID(w, r)
	if !ok {
		return
	}
	h.save(w, r, id)
}

func (h WikiHandler) save(w http.ResponseWriter, r *http.Request, id ulid.ULID) {
	var req payloads.WikiSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	res, err := h.Save.Execute(r.Context(), usecases.WikiPageInput{
		ID: id, Title: req.Title, Tags: req.Tags, Body: req.Body,
	})
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if res.NeedsReset && h.OnReprocess != nil {
		h.OnReprocess(res.Hash)
	} else if h.OnChanged != nil {
		h.OnChanged(res.Page.ID.String(), res.Hash)
	}
	if h.URLs != nil && len(res.URLsAdded) > 0 {
		h.URLs.ScheduleWikiURLs(res.Page.ID.String(), res.URLsAdded)
	}
	if h.OnCleanup != nil {
		h.OnCleanup(res.Page.ID.String(), res.StaleSourceHash)
	}
	// Only a changed PROSE needs new tags: a save that moved a tag, or re-saved an unchanged page, must
	// not spend a model call on it.
	if h.OnTag != nil && res.Retag {
		h.OnTag(res.Page.ID.String())
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	payloads.WriteJSON(w, status, wikiPagePayload(res.Page))
}

// Delete serves DELETE /api/v1/wiki/{id}.
func (h WikiHandler) DeletePage(w http.ResponseWriter, r *http.Request) {
	id, ok := wikiID(w, r)
	if !ok {
		return
	}
	hash, removedURLs, err := h.Delete.Execute(r.Context(), id)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnDeleted != nil && hash != "" {
		h.OnDeleted(hash)
	}
	if h.OnURLRemoved != nil {
		for _, urlHash := range removedURLs {
			h.OnURLRemoved(urlHash)
		}
	}
	payloads.WriteJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

func wikiID(w http.ResponseWriter, r *http.Request) (ulid.ULID, bool) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "wiki id must be a ULID", types.ErrNotFound))
		return ulid.ULID{}, false
	}
	return id, true
}

func wikiPagePayload(page entities.WikiPage) payloads.WikiPage {
	return payloads.WikiPage{
		ID: page.ID.String(), Title: page.Title, Tags: page.Tags, AutoTags: page.AutoTags,
		URLs: page.URLs, Body: page.Body,
		SourceHash: page.SourceHash, FavoriteAt: favoriteTime(page.FavoriteAt),
		CreatedAt: page.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt: page.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func favoriteTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
