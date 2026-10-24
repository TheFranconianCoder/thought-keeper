package gateways

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// TestReasoningContentRoundTrip: a thinking model rejects a conversation whose assistant
// turns lost their `reasoning_content` ("The reasoning_content in the thinking mode must be
// passed back to the API"). The gateway has to keep it in both directions.
func TestReasoningContentRoundTrip(t *testing.T) {
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body.Messages...)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"1","object":"chat.completion","model":"m",
			"choices":[{"index":0,"finish_reason":"tool_calls","message":{
				"role":"assistant","content":"","reasoning_content":"weighing the options",
				"tool_calls":[{"id":"c1","type":"function","function":{"name":"t","arguments":"{}"}}]}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	t.Cleanup(srv.Close)

	gw, ok := NewOpenAIAgent(OpenAIConfig{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if !ok {
		t.Fatal("gateway not configured")
	}
	req := interfaces.AgentRequest{
		Model: "m",
		Messages: []interfaces.AgentMessage{
			{Role: "system", Content: "sys"},
			{
				Role: "assistant", Content: "on it",
				ReasoningContent: "weighing the options",
				ToolCalls:        []interfaces.AgentToolCall{{ID: "c0", Name: "t", Arguments: "{}"}},
			},
			{Role: "tool", ToolCallID: "c0", Name: "t", Content: "ok"},
			{Role: "user", Content: "continue"},
		},
	}
	resp, err := gw.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Outbound: the assistant turn carries its reasoning back to the API.
	var found bool
	for _, m := range sent {
		if m["role"] == "assistant" && m["reasoning_content"] == "weighing the options" {
			found = true
		}
	}
	if !found {
		t.Errorf("reasoning_content was not sent back: %+v", sent)
	}

	// Inbound: the response's reasoning is kept for the next turn.
	if resp.Message.ReasoningContent != "weighing the options" {
		t.Errorf("reasoning content = %q, want it captured", resp.Message.ReasoningContent)
	}
	if len(resp.Message.ToolCalls) != 1 || !strings.EqualFold(resp.Message.ToolCalls[0].Name, "t") {
		t.Errorf("tool calls = %+v", resp.Message.ToolCalls)
	}
}

// TestEmptyReasoningContentIsStillSent: a thinking model returns `reasoning_content: ""` on a
// tool-call turn. The SDK's omitempty drops the key; the transport must put it back, or the NEXT
// request is rejected even though the first two turns worked.
func TestEmptyReasoningContentIsStillSent(t *testing.T) {
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body.Messages...)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"deepseek-v4.1-flash","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)

	gw, ok := NewOpenAIAgent(OpenAIConfig{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if !ok {
		t.Fatal("gateway not configured")
	}
	_, err := gw.Complete(context.Background(), interfaces.AgentRequest{
		Model: "deepseek-v4.1-flash",
		Messages: []interfaces.AgentMessage{
			{Role: "user", Content: "hi"},
			{
				Role: "assistant", Content: "(tool call)", ReasoningContent: "",
				ToolCalls: []interfaces.AgentToolCall{{ID: "c1", Name: "t", Arguments: "{}"}},
			},
			{Role: "tool", ToolCallID: "c1", Name: "t", Content: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	for _, m := range sent {
		if m["role"] != "assistant" {
			continue
		}
		if _, present := m["reasoning_content"]; !present {
			t.Fatalf("assistant message lost reasoning_content: %+v", m)
		}
	}
}
