package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// InterestLister lists the reader's rules and their queue shares.
type InterestLister interface {
	Execute(ctx context.Context) ([]usecases.InterestView, error)
}

// InterestSaver creates or updates a rule.
type InterestSaver interface {
	Execute(ctx context.Context, slug string, in usecases.InterestInput) (entities.Interest, error)
}

// InterestDeleter removes a rule.
type InterestDeleter interface {
	Execute(ctx context.Context, slug string) error
}

// InterestPreviewer calibrates a draft against the vault.
type InterestPreviewer interface {
	Execute(ctx context.Context, in usecases.InterestInput) (usecases.InterestPreview, error)
}

// InterestRejecter records a reader's correction for one source and one rule.
type InterestRejecter interface {
	Execute(ctx context.Context, slug, hash string, accepted bool) error
}

// InterestHandler serves the interest endpoints: the manager, its calibration preview and the
// per-source correction. None of them run on the ingest path.
type InterestHandler struct {
	Lister    InterestLister
	Saver     InterestSaver
	Deleter   InterestDeleter
	Previewer InterestPreviewer
	Rejecter  InterestRejecter
}

// List serves GET /api/v1/interests.
func (h InterestHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Lister == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	views, err := h.Lister.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.InterestListFromUseCase(views))
}

// Post serves POST /api/v1/interests.
func (h InterestHandler) Post(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInterest(w, r, true)
	if !ok {
		return
	}
	if h.Saver == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	rule, err := h.Saver.Execute(r.Context(), "", in)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusCreated, payloads.InterestFromEntity(rule, 0))
}

// Put serves PUT /api/v1/interests/{slug}.
func (h InterestHandler) Put(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if slug == "" {
		payloads.WriteError(w, types.New("invalid_interest", "an interest slug is required", nil))
		return
	}
	in, ok := decodeInterest(w, r, false)
	if !ok {
		return
	}
	rule, err := h.Saver.Execute(r.Context(), slug, in)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.InterestFromEntity(rule, 0))
}

// Delete serves DELETE /api/v1/interests/{slug}.
func (h InterestHandler) Delete(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if err := h.Deleter.Execute(r.Context(), slug); err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, map[string]string{"slug": slug})
}

// Preview serves POST /api/v1/interests/preview: the draft evaluated against the vault, no write.
func (h InterestHandler) Preview(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInterest(w, r, true)
	if !ok {
		return
	}
	if h.Previewer == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	res, err := h.Previewer.Execute(r.Context(), in)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.InterestPreviewFromUseCase(res))
}

// PutSource serves PUT /api/v1/interests/{slug}/sources/{hash}: "yes, it fits" or "not this
// interest". It is the correction channel the rail offers while reading.
func (h InterestHandler) PutSource(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	hash := strings.TrimSpace(chi.URLParam(r, "hash"))
	var req payloads.InterestReject
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	if err := h.Rejecter.Execute(r.Context(), slug, hash, req.Accepted); err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, map[string]any{"slug": slug, "hash": hash, "accepted": req.Accepted})
}

// decodeInterest reads the editor's body. A new rule is enabled unless it says otherwise; a draft
// preview has no pause state at all.
func decodeInterest(w http.ResponseWriter, r *http.Request, creating bool) (usecases.InterestInput, bool) {
	var req payloads.InterestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return usecases.InterestInput{}, false
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	} else if !creating {
		enabled = true
	}
	return usecases.InterestInput{
		Name: req.Name, Terms: req.Terms, Exclude: req.Exclude, Kinds: req.Kinds,
		Enabled: enabled, QueueExisting: req.QueueExisting,
	}, true
}
