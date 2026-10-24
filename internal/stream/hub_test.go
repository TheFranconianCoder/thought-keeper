package stream

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func next(t *testing.T, s *Subscriber) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msg, ok := s.Next(ctx)
	if !ok {
		t.Fatal("no event received")
	}
	var ev Event
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ev
}

func TestHubPublishesToSubscriber(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	defer h.Unsubscribe(s)

	h.Publish("run", map[string]any{"run_id": "01ABC", "status": "running"})

	ev := next(t, s)
	if ev.Type != "run" || ev.At == "" {
		t.Errorf("event = %+v", ev)
	}
	if h.Subscribers() != 1 {
		t.Errorf("subscribers = %d, want 1", h.Subscribers())
	}
}

func TestHubPublishNeverBlocks(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	defer h.Unsubscribe(s)

	done := make(chan struct{})
	go func() {
		for i := range 5000 {
			h.Publish("log", map[string]any{"hash": "ab", "line": "…"})
			h.Publish("progress", map[string]any{"hash": "ab", "fields": map[string]any{"atoms": i}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}

// TestHubNeverDropsAStateEvent is the regression for a stale UI: a flood of log lines and
// progress must not push a state event out of the queue. "Drop when full" lost exactly the
// events that heal the UI, and there is no polling to recover from that.
func TestHubNeverDropsAStateEvent(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	defer h.Unsubscribe(s)

	// Nobody reads while the flood happens: this is a browser tab that is busy rendering.
	for range 2000 {
		h.Publish("log", map[string]any{"hash": "ab", "line": "line"})
	}
	h.Publish("source", map[string]any{"hash": "ab"})
	h.Publish("clusters", map[string]any{"hash": "ab"})
	for range 2000 {
		h.Publish("log", map[string]any{"hash": "ab", "line": "line"})
	}
	h.Publish("run", map[string]any{"run_id": "01ABC", "status": "succeeded"})

	// Drain: every state event must come out, whatever the log tail did.
	seen := map[string]int{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for s.Pending() > 0 {
		msg, ok := s.Next(ctx)
		if !ok {
			break
		}
		var ev Event
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Type != "log" {
			seen[ev.Type]++
		}
	}
	for _, typ := range []string{"source", "clusters", "run"} {
		if seen[typ] != 1 {
			t.Errorf("%s events = %d, want exactly 1 (state must never be dropped)\nseen: %v", typ, seen[typ], seen)
		}
	}
}

// TestHubCoalescesStateEvents: the client reacts to a state event by re-reading the container
// it names, so fifty "the cluster layer changed" events carry exactly the information of one.
func TestHubCoalescesStateEvents(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	defer h.Unsubscribe(s)

	for i := range 50 {
		h.Publish("clusters", map[string]any{"hash": "ab", "pages": i})
	}
	h.Publish("progress", map[string]any{"hash": "ab", "fields": map[string]any{"atoms": 1}})
	h.Publish("progress", map[string]any{"hash": "ab", "fields": map[string]any{"atoms": 2}})

	first := next(t, s)
	if first.Type != "clusters" {
		t.Fatalf("first event = %s, want clusters", first.Type)
	}
	// The newest payload wins: coalescing keeps the latest state, not the first one.
	data, _ := first.Data.(map[string]any)
	if pages, ok := data["pages"].(float64); !ok || pages != 49 {
		t.Errorf("coalesced payload = %v, want the newest (pages=49)", first.Data)
	}
	second := next(t, s)
	if second.Type != "progress" {
		t.Fatalf("second event = %s, want progress", second.Type)
	}
	data, _ = second.Data.(map[string]any)
	fields, _ := data["fields"].(map[string]any)
	if atoms, ok := fields["atoms"].(float64); !ok || atoms != 2 {
		t.Errorf("coalesced progress = %v, want the newest (atoms=2)", second.Data)
	}
	// Nothing else follows: 50 cluster events and 2 progress events are 2 frames.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if msg, ok := s.Next(ctx); ok {
		t.Fatalf("unexpected extra event: %s", msg)
	}
}

// TestHubKeepsTheNewestLogLines: the log is a live tail — when it is cut, it is cut at the
// front, so what the overlay shows is what the run is doing now.
func TestHubKeepsTheNewestLogLines(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	defer h.Unsubscribe(s)

	for i := range maxLogLines + 50 {
		h.Publish("log", map[string]any{"hash": "ab", "line": string(rune('a' + i%26))})
	}
	lines := 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for s.Pending() > 0 {
		msg, ok := s.Next(ctx)
		if !ok {
			break
		}
		var ev Event
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Type == "log" {
			lines++
		}
	}
	if lines != maxLogLines {
		t.Errorf("log lines = %d, want the tail bounded at %d", lines, maxLogLines)
	}
}

func TestHubUnsubscribeRemovesSubscriber(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	if h.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", h.Subscribers())
	}
	s.Close()
	s.Close() // safe twice
	if h.Subscribers() != 0 {
		t.Errorf("subscribers = %d, want 0", h.Subscribers())
	}
	// A closed subscriber's Next returns immediately.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, ok := s.Next(ctx); ok {
		t.Error("Next returned an event after Close")
	}
}
