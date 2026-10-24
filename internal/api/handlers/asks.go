package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// AskLister lists saved Ask conversations.
type AskLister interface {
	Execute(ctx context.Context, limit, offset int) ([]partialentities.PartialAsk, error)
}

// AskGetter returns one saved Ask.
type AskGetter interface {
	Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialAsk, error)
}

// AskDeleter deletes one saved Ask.
type AskDeleter interface {
	Execute(ctx context.Context, id ulid.ULID) error
}

// AskHandler serves the saved Ask area. Answers are written automatically by the query use case;
// this handler only lists, reads and deletes.
type AskHandler struct {
	Lister  AskLister
	Getter  AskGetter
	Deleter AskDeleter
}

// List serves GET /api/v1/asks.
func (h AskHandler) List(w http.ResponseWriter, r *http.Request) {
	asks, err := h.Lister.Execute(r.Context(), atoiDefault(r.URL.Query().Get("limit"), 50), atoiDefault(r.URL.Query().Get("offset"), 0))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := make([]payloads.AskSummary, 0, len(asks))
	for _, a := range asks {
		out = append(out, payloads.AskSummary{
			ID: a.ID.String(), Question: a.Question, Tags: a.Tags,
			CreatedAt: a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt: a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.AskList{Asks: out, Count: len(out)})
}

// Get serves GET /api/v1/asks/{id}.
func (h AskHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	a, err := h.Getter.Execute(r.Context(), id)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.Ask{
		ID: a.ID.String(), Question: a.Question, Answer: a.Answer, Tags: a.Tags,
		CreatedAt: a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// Delete serves DELETE /api/v1/asks/{id}.
func (h AskHandler) DeleteOne(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	if err := h.Deleter.Execute(r.Context(), id); err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}
