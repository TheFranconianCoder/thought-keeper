package handlers

import (
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ConfigProvider yields the running configuration for the Config overlay.
type ConfigProvider interface {
	Config() payloads.Config
}

// ConfigHandler serves GET /api/v1/config.
type ConfigHandler struct {
	Provider ConfigProvider
}

// Get answers the running configuration. A router built without a provider (tests) answers 404
// instead of panicking on the nil interface.
func (h ConfigHandler) Get(w http.ResponseWriter, _ *http.Request) {
	if h.Provider == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, h.Provider.Config())
}
