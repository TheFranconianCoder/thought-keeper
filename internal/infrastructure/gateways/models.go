package gateways

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ListModels queries an OpenAI-compatible endpoint's model catalogue. It is a
// development aid for choosing model_compile / model_reason.
func ListModels(ctx context.Context, cfg OpenAIConfig) ([]string, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no OpenAI API key configured (set OPENAI_API_KEY)")
	}
	base := cfg.BaseURL
	if base == "" {
		base = openai.DefaultConfig(cfg.APIKey).BaseURL
	}
	url := strings.TrimRight(base, "/") + "/models"

	client := &http.Client{
		Transport: &headerTransport{base: http.DefaultTransport, headers: resolveHeaders(cfg.BaseURL, cfg.Headers)},
	}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	if _, ok := cfg.Headers["x-opencode-session"]; !ok && req.Header.Get("x-opencode-session") == "" {
		req.Header.Set("x-opencode-session", randomSessionID())
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("list models: decode: %w", err)
	}
	out := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		out = append(out, m.ID)
	}
	return out, nil
}
