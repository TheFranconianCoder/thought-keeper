// Package handlers contains the HTTP handlers. Handlers parse, call one use case
// (or a provider) and map the result — nothing more.
package handlers

import (
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
)

// StatusProvider yields the current doctor report.
type StatusProvider interface {
	Status() payloads.Status
}

// StatusHandler serves GET /api/v1/status.
type StatusHandler struct {
	Provider StatusProvider
}

func (h StatusHandler) Get(w http.ResponseWriter, _ *http.Request) {
	payloads.WriteJSON(w, http.StatusOK, h.Provider.Status())
}
