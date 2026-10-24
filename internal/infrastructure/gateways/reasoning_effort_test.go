package gateways

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// TestReasoningEffortIsSent: "minimal thinking" is a request parameter, and we send it.
func TestReasoningEffortIsSent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	t.Cleanup(srv.Close)
	gw, _ := NewOpenAIAgent(OpenAIConfig{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if _, err := gw.Complete(context.Background(), interfaces.AgentRequest{
		Model: "m", Messages: []interfaces.AgentMessage{{Role: "user", Content: "hi"}}, ReasoningEffort: "minimal",
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got["reasoning_effort"] != "minimal" {
		t.Errorf("reasoning_effort = %v, want minimal", got["reasoning_effort"])
	}
}

// TestUnknownReasoningEffortIsRetried: an endpoint that does not know the parameter must not
// break the run — the call is repeated once without it.
func TestUnknownReasoningEffortIsRetried(t *testing.T) {
	var attempts []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		attempts = append(attempts, body)
		if _, ok := body["reasoning_effort"]; ok {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unsupported parameter: reasoning_effort"}}`))
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	t.Cleanup(srv.Close)
	gw, _ := NewOpenAIAgent(OpenAIConfig{APIKey: "k", BaseURL: srv.URL + "/v1"})
	resp, err := gw.Complete(context.Background(), interfaces.AgentRequest{
		Model: "m", Messages: []interfaces.AgentMessage{{Role: "user", Content: "hi"}}, ReasoningEffort: "minimal",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Message.Content != "ok" {
		t.Errorf("content = %q", resp.Message.Content)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (with and without the parameter)", len(attempts))
	}
	if _, ok := attempts[1]["reasoning_effort"]; ok {
		t.Error("the retry still carried reasoning_effort")
	}
}
