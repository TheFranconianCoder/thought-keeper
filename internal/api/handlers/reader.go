package handlers

import (
	"context"
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ReaderStateProvider returns the shared seen watermark without acknowledging anything.
type ReaderStateProvider func(ctx context.Context) (entities.ReaderState, error)

// ReaderActivator acknowledges everything that happened up to now — arrivals and content changes —
// and returns the baseline the client should mark from.
type ReaderActivator func(ctx context.Context) (entities.ReaderActivation, error)

// ReaderNotifier broadcasts a watermark change to every connected client, so the sources another
// client just acknowledged stop being new here too.
type ReaderNotifier interface {
	Publish(typ string, data any)
}

// ReaderHandler serves the shared "new" watermark.
type ReaderHandler struct {
	State     ReaderStateProvider
	Activator ReaderActivator
	Notifier  ReaderNotifier
}

// Get serves GET /api/v1/reader.
func (h ReaderHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.State == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	st, err := h.State(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.ReaderStateFromUseCase(st))
}

// Activate serves POST /api/v1/reader/active. It is the client saying "I am active and showing the
// new sources and the changed pages"; the answer carries the baseline its marks use. The watermark
// only moves when there is something later than it, so an activation with nothing new publishes
// nothing.
func (h ReaderHandler) Activate(w http.ResponseWriter, r *http.Request) {
	if h.Activator == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	act, err := h.Activator(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := payloads.ReaderActivationFromUseCase(act)
	if h.Notifier != nil && act.Changed {
		h.Notifier.Publish("reader", map[string]any{"seen_at": out.SeenAt, "first": out.First})
	}
	payloads.WriteJSON(w, http.StatusOK, out)
}
