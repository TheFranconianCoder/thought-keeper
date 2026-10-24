package gateways

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// reasoningTransport keeps the `reasoning_content` key on assistant messages even when its value is
// empty. The OpenAI SDK marshals that field with `omitempty`, so an assistant turn that thought
// nothing (or whose provider returned `reasoning_content: ""`) loses the KEY entirely — and a
// thinking endpoint (DeepSeek and compatible proxies) rejects the whole conversation with
// "The `reasoning_content` in the thinking mode must be passed back to the API." The first two
// turns then work and the third fails, which looks like a random model error.
//
// The mutation is deliberately narrow: chat completions only, assistant messages only, and only
// when the model is a known reasoning model. A non-thinking endpoint never sees the field.
type reasoningTransport struct {
	base http.RoundTripper
}

func (t *reasoningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Body == nil || !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		return base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	rewritten := body
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		model, _ := payload["model"].(string)
		if isThinkingModel(model) {
			if msgs, ok := payload["messages"].([]any); ok {
				changed := false
				for _, raw := range msgs {
					msg, ok := raw.(map[string]any)
					if !ok || msg["role"] != "assistant" {
						continue
					}
					if _, present := msg["reasoning_content"]; !present {
						msg["reasoning_content"] = ""
						changed = true
					}
				}
				if changed {
					if encoded, mErr := json.Marshal(payload); mErr == nil {
						rewritten = encoded
					}
				}
			}
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(rewritten))
	req.ContentLength = int64(len(rewritten))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewritten)), nil
	}
	return base.RoundTrip(req)
}

// isThinkingModel reports whether a model speaks the thinking protocol that requires
// `reasoning_content` to be echoed on every assistant turn. It is intentionally a name check:
// there is no capability flag in the OpenAI-compatible wire format.
func isThinkingModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	// This is the DeepSeek `reasoning_content` extension. Do not include OpenAI's o-series: it uses
	// a different reasoning protocol, and an unknown field there would be another rejection.
	return strings.Contains(m, "deepseek") || strings.Contains(m, "reasoner") || strings.Contains(m, "thinking")
}
