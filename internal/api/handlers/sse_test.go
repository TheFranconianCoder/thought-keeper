package handlers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/handlers"
	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

// TestSSEStreamDeliversStateAfterAFlood drives the real handler over HTTP: a flood of log
// lines must not make a state event vanish on the wire. This is the whole reason the UI used
// to stay stale — the frame that would have healed it never arrived.
func TestSSEStreamDeliversStateAfterAFlood(t *testing.T) {
	hub := stream.NewHub()
	h := handlers.SSEHandler{Hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(h.Stream))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	// Wait for the connection to be established (the handler sends a comment first), then
	// flood while nobody reads the body — a busy browser tab.
	time.Sleep(50 * time.Millisecond)
	for range 3000 {
		hub.Publish("log", map[string]any{"hash": "ab", "line": "line"})
	}
	hub.Publish("clusters", map[string]any{"hash": "ab", "clusters": 3})
	for range 3000 {
		hub.Publish("log", map[string]any{"hash": "ab", "line": "line"})
	}
	hub.Publish("run", map[string]any{"run_id": "01ABC", "op": "pipeline", "status": "succeeded"})

	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seen := map[string]bool{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (!seen["clusters"] || !seen["run"]) {
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev stream.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if ev.Type != "log" {
			seen[ev.Type] = true
		}
	}
	if !seen["clusters"] || !seen["run"] {
		t.Fatalf("state events lost on the wire: seen=%v", seen)
	}
}

// TestSSEStreamStopsWhenTheClientLeaves: closing the connection must not leak the subscriber.
func TestSSEStreamStopsWhenTheClientLeaves(t *testing.T) {
	hub := stream.NewHub()
	h := handlers.SSEHandler{Hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(h.Stream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if hub.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", hub.Subscribers())
	}
	cancel()
	_ = res.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hub.Subscribers() != 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if hub.Subscribers() != 0 {
		t.Errorf("subscribers = %d after the client left, want 0", hub.Subscribers())
	}
}
