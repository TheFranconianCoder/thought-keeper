package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ConfigProvider yields the running configuration for the Config overlay.
type ConfigProvider interface {
	Config() payloads.Config
}

// ConfigWriter applies a patch from the overlay. The app maps the config package's errors to the
// wire (400 for a key that is not writable, 422 for a value it cannot use).
type ConfigWriter interface {
	WriteConfig(payloads.ConfigWrite) (payloads.ConfigWriteResult, error)
	ReloadConfig() (payloads.ConfigWriteResult, error)
}

// ConfigHandler serves GET and PUT /api/v1/config.
type ConfigHandler struct {
	Provider ConfigProvider
	Writer   ConfigWriter
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

// Put serves PUT /api/v1/config. The file is written first (the vault's config is the truth); the
// apply is queued in-process, so the answer says so and the overlay follows the running view.
func (h ConfigHandler) Put(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	var in payloads.ConfigWrite
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "the request is not a config patch", nil))
		return
	}
	res, err := h.Writer.WriteConfig(in)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, res)
}

// Reload serves POST /api/v1/config/reload: apply the file as it is on disk (the SSH-edit path).
func (h ConfigHandler) Reload(w http.ResponseWriter, _ *http.Request) {
	if h.Writer == nil {
		payloads.WriteError(w, types.ErrNotFound)
		return
	}
	res, err := h.Writer.ReloadConfig()
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, res)
}
