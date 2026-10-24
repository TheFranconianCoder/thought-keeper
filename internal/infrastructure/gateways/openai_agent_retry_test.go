package gateways_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
)

// flakyEndpoint fails the first n requests with the given status, then answers.
func flakyEndpoint(t *testing.T, failures int, status int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if int(n) <= failures {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "boom", "type": "server_error"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestGatewayRetriesATransientFailure: EVERY model call goes through the gateway (the tree title, the
// taxonomy, each agent turn), so a timeout or a 5xx must be absorbed here — one slow call must not
// end a run that has already derived most of a document.
func TestGatewayRetriesATransientFailure(t *testing.T) {
	srv, calls := flakyEndpoint(t, 2, http.StatusInternalServerError)
	gw, ok := gateways.NewOpenAIAgent(gateways.OpenAIConfig{
		BaseURL: srv.URL, APIKey: "test", Timeout: 5 * time.Second,
		Retry: interfaces.RetryPolicy{Attempts: 3, Base: time.Millisecond},
	})
	if !ok {
		t.Fatal("gateway not built")
	}
	var retries []time.Duration
	resp, err := gw.Complete(context.Background(), interfaces.AgentRequest{
		Model: "test",
		OnRetry: func(_ int, wait time.Duration, err error) {
			retries = append(retries, wait)
			if err == nil {
				t.Error("a retry was reported without an error")
			}
		},
	})
	if err != nil {
		t.Fatalf("a transient failure ended the call: %v", err)
	}
	if resp.Message.Content != "ok" {
		t.Errorf("content = %q, want the answer after the retries", resp.Message.Content)
	}
	if *calls != 3 {
		t.Errorf("endpoint calls = %d, want 3 (two failures, one answer)", *calls)
	}
	if len(retries) != 2 || retries[0] >= retries[1] {
		t.Errorf("reported waits = %v, want two, growing", retries)
	}
}

// TestGatewayDoesNotRetryAPermanentFailure: a rejected key must fail fast. Retrying it would only
// burn quota and hide the real problem.
func TestGatewayDoesNotRetryAPermanentFailure(t *testing.T) {
	srv, calls := flakyEndpoint(t, 10, http.StatusUnauthorized)
	gw, _ := gateways.NewOpenAIAgent(gateways.OpenAIConfig{
		BaseURL: srv.URL, APIKey: "test", Timeout: 5 * time.Second,
		Retry: interfaces.RetryPolicy{Attempts: 3, Base: time.Millisecond},
	})
	notices := 0
	_, err := gw.Complete(context.Background(), interfaces.AgentRequest{
		Model:   "test",
		OnRetry: func(int, time.Duration, error) { notices++ },
	})
	if err == nil {
		t.Fatal("a 401 was reported as success")
	}
	if *calls != 1 {
		t.Errorf("endpoint calls = %d, want 1 (no retry for a permanent failure)", *calls)
	}
	if notices != 0 {
		t.Errorf("retry notices = %d, want 0", notices)
	}
	if interfaces.IsTransient(err) {
		t.Error("a 401 was classified as transient — the caller would retry it")
	}
}
