package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ReaderStateProvider returns the seen watermark without acknowledging anything.
type ReaderStateProvider func(ctx context.Context) (entities.ReaderState, error)

// ReaderActivator answers the lens that became active with the baseline it marks from, and lets the
// list acknowledge the batch.
type ReaderActivator func(ctx context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error)

// ReaderNotifier broadcasts a watermark change to every connected client, so the sources another
// client just acknowledged stop being new here too.
type ReaderNotifier interface {
	Publish(typ string, data any)
}

// ReaderHandler serves the vault's seen watermark.
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

// Activate serves POST /api/v1/reader/active?scope=sources|start. It is the client saying "I am
// active and showing this lens"; the answer carries the baseline that lens marks from. Only the
// Sources list moves the watermark; the Start lens only summarizes the batch, so its call writes
// nothing — and an older client that omits the scope can never clear the list's marks. One moved
// watermark publishes one `reader` frame; a call that changed nothing publishes none.
func (h ReaderHandler) Activate(w http.ResponseWriter, r *http.Request) {
	if h.Activator == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	scope := entities.ReaderScope(strings.TrimSpace(r.URL.Query().Get("scope")))
	if scope == "" {
		scope = entities.ReaderScopeStart
	}
	if !scope.Valid() {
		payloads.WriteError(w, types.New("invalid_body", "scope must be start or sources", nil))
		return
	}
	act, err := h.Activator(r.Context(), scope)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := payloads.ReaderActivationFromUseCase(act)
	if h.Notifier != nil && act.SeenAt.After(act.Since) {
		data := map[string]any{"scope": string(act.Scope), "seen_at": out.SeenAt}
		if act.First {
			data["first"] = true
		}
		h.Notifier.Publish("reader", data)
	}
	payloads.WriteJSON(w, http.StatusOK, out)
}
