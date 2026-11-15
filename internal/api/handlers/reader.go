package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ReaderStateProvider returns both seen watermarks without acknowledging anything.
type ReaderStateProvider func(ctx context.Context) (entities.ReaderState, error)

// ReaderActivator acknowledges everything that happened up to now for ONE scope — Start's statistic
// or the Sources marks — and returns the baseline that scope should mark from.
type ReaderActivator func(ctx context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error)

// ReaderNotifier broadcasts a watermark change to every connected client, so the sources another
// client just acknowledged stop being new here too.
type ReaderNotifier interface {
	Publish(typ string, data any)
}

// ReaderHandler serves the vault's two seen watermarks.
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

// Activate serves POST /api/v1/reader/active?scope=start|sources. It is the client saying "I am
// active and showing this lens"; the answer carries the baseline that lens marks from. The
// watermark only moves when there is something later than it, so an activation with nothing new
// publishes nothing. An absent scope means Start: a stale client keeps acknowledging something,
// and it can never clear the Sources marks by opening the front door.
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
	if h.Notifier != nil && act.Changed {
		h.Notifier.Publish("reader", map[string]any{
			"scope":   out.Scope,
			"seen_at": out.SeenAt,
			"first":   out.First,
		})
	}
	payloads.WriteJSON(w, http.StatusOK, out)
}
