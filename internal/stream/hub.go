// Package stream is an in-process publish/subscribe hub for server-sent events. The
// PWA subscribes once over SSE and receives updates instead of polling.
//
// The hub distinguishes two kinds of events, and that distinction is the whole point:
//
//   - STATE events (`run`, `source`, `clusters`, `findings`, `urls`) tell the client that a
//     container changed. They are NEVER dropped. Because the client reacts by re-reading
//     that container from the API, only the newest event of a type carries information —
//     so they coalesce per type instead of queueing up.
//   - Progress events coalesce per source (they are counts the client merges), and log
//     lines are a live tail: when a subscriber falls behind, old lines fall off the front.
//
// A dropped state event used to mean a stale UI forever (there is no polling to heal it),
// so "drop when the buffer is full" was wrong for exactly the events that matter.
package stream

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Event is one server-sent event.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	At   string `json:"at"`
}

const (
	// maxLogLines bounds how far behind a subscriber may fall on the live log tail.
	maxLogLines = 256
	// maxStateEvents bounds the coalesced queue. With five state types plus one progress
	// slot per source this is generous; hitting it drops the oldest slot, never the newest.
	maxStateEvents = 128
)

// Hub fans events out to every subscriber. Publishing never blocks: one stuck browser
// tab cannot stall a run.
type Hub struct {
	mu   sync.Mutex
	subs map[*Subscriber]struct{}
}

// NewHub builds an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[*Subscriber]struct{})}
}

// Subscribe registers a new subscriber. The caller must Close it (Unsubscribe does that).
func (h *Hub) Subscribe() *Subscriber {
	s := &Subscriber{hub: h, wake: make(chan struct{}, 1), stateAt: map[string]int{}}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Unsubscribe removes a subscriber. Safe to call more than once.
func (h *Hub) Unsubscribe(s *Subscriber) { s.Close() }

// Publish marshals and queues one event for every subscriber.
func (h *Hub) Publish(typ string, data any) {
	msg, err := json.Marshal(Event{Type: typ, Data: data, At: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return
	}
	key := coalesceKey(typ, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		s.push(key, msg)
	}
}

// Subscribers reports the current subscriber count (for the status report).
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// coalesceKey names the slot an event belongs to. An empty key means "not coalescable"
// (a log line, each of which says something different).
func coalesceKey(typ string, data any) string {
	switch typ {
	case "run", "source", "clusters", "findings", "urls":
		return typ
	case "progress":
		if m, ok := data.(map[string]any); ok {
			if hash, ok := m["hash"].(string); ok {
				return "progress#" + hash
			}
		}
		return "progress"
	}
	return ""
}

// Subscriber is one SSE client: a coalesced state queue plus a bounded log tail.
type Subscriber struct {
	hub *Hub

	mu      sync.Mutex
	state   []queued
	stateAt map[string]int // key -> index in state (coalescing)
	logs    [][]byte       // ring: the newest maxLogLines lines
	closed  bool
	wake    chan struct{}
}

type queued struct {
	key string
	msg []byte
}

// push queues one event. Never blocks; a full log tail loses its oldest line.
func (s *Subscriber) push(key string, msg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if key == "" {
		if len(s.logs) >= maxLogLines {
			// A live tail that lags is worse than one that cuts: drop the oldest line.
			s.logs = append(s.logs[:0], s.logs[1:]...)
		}
		s.logs = append(s.logs, msg)
	} else if i, ok := s.stateAt[key]; ok {
		s.state[i].msg = msg // coalesce: only the newest state of a kind matters
	} else {
		if len(s.state) >= maxStateEvents {
			s.dropOldest()
		}
		s.stateAt[key] = len(s.state)
		s.state = append(s.state, queued{key: key, msg: msg})
	}
	s.signal()
}

// dropOldest makes room for a newer slot, losing the least recent one.
func (s *Subscriber) dropOldest() {
	if len(s.state) == 0 {
		return
	}
	delete(s.stateAt, s.state[0].key)
	s.state = s.state[1:]
	for k, i := range s.stateAt {
		s.stateAt[k] = i - 1
	}
}

func (s *Subscriber) pop() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// State first: it is what the UI is waiting for. The log tail drains right after.
	if len(s.state) > 0 {
		it := s.state[0]
		s.state = s.state[1:]
		delete(s.stateAt, it.key)
		for k, i := range s.stateAt {
			s.stateAt[k] = i - 1
		}
		return it.msg, true
	}
	if len(s.logs) > 0 {
		msg := s.logs[0]
		s.logs = s.logs[1:]
		return msg, true
	}
	return nil, false
}

// Pending reports how many events are queued for this subscriber.
func (s *Subscriber) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.state) + len(s.logs)
}

// Next returns the next queued event, blocking until one arrives, the context ends or the
// subscriber is closed.
func (s *Subscriber) Next(ctx context.Context) ([]byte, bool) {
	for {
		if msg, ok := s.pop(); ok {
			return msg, true
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return nil, false
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-s.wake:
		}
	}
}

// Close removes the subscriber from the hub and releases a waiting Next.
func (s *Subscriber) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.hub.remove(s)
	s.signal()
}

func (s *Subscriber) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (h *Hub) remove(s *Subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}
