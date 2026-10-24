package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
)

// endpoint is the runtime record that lets the CLI find the running instance.
type endpoint struct {
	Addr string `json:"addr"`
	PID  int    `json:"pid"`
}

// writeEndpoint publishes the running instance address to the runtime file.
func writeEndpoint(addr string, pid int) error {
	if err := os.MkdirAll(config.RuntimeDir(), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(endpoint{Addr: addr, PID: pid})
	if err != nil {
		return err
	}
	return os.WriteFile(config.EndpointPath(), b, 0o600)
}

// ReadEndpoint returns the published address of a running instance.
func ReadEndpoint() (string, error) {
	b, err := os.ReadFile(config.EndpointPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("no running instance")
		}
		return "", err
	}
	var e endpoint
	if err := json.Unmarshal(b, &e); err != nil {
		return "", err
	}
	return e.Addr, nil
}

func removeEndpoint() { _ = os.Remove(config.EndpointPath()) }

// Ping reports whether a Thought Keeper instance answers on addr. It is used to
// validate a published endpoint before telling the user "already running".
func Ping(addr string) error {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/api/v1/status")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("instance answered %d", resp.StatusCode)
	}
	return nil
}
