package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

func nextEvent(t *testing.T, s *stream.Subscriber) stream.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msg, ok := s.Next(ctx)
	if !ok {
		t.Fatal("no event received")
	}
	var ev stream.Event
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ev
}

// A successful write announces itself to every client; a read does not, and neither does a write that
// failed. That is what keeps a second window (or a CLI reindex) from leaving the UI stale.
func TestPublishWritesAnnouncesSuccessfulWrites(t *testing.T) {
	hub := stream.NewHub()
	sub := hub.Subscribe()
	defer hub.Unsubscribe(sub)

	r := chi.NewRouter()
	r.Use(publishWrites(hub))
	r.Post("/api/v1/reindex", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Post("/api/v1/sources/{hash}/distill", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	})
	r.Get("/api/v1/notes", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Post("/api/v1/window/close", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	post := func(path string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	}

	// A write: every affected container, each once.
	post("/api/v1/reindex")
	types := map[string]bool{}
	for range 5 {
		types[nextEvent(t, sub).Type] = true
	}
	for _, want := range []string{"source", "clusters", "findings", "pipeline", "wiki"} {
		if !types[want] {
			t.Errorf("no %q event after a write (got %v)", want, types)
		}
	}
	if sub.Pending() != 0 {
		t.Errorf("more events than containers: %d pending", sub.Pending())
	}

	// A write that failed changed nothing.
	post("/api/v1/sources/abc/distill")
	if n := sub.Pending(); n != 0 {
		t.Errorf("a rejected write announced itself (%d events)", n)
	}

	// A read changes nothing either.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil))
	if n := sub.Pending(); n != 0 {
		t.Errorf("a read announced itself (%d events)", n)
	}

	// The window channel is the shell talking to itself.
	post("/api/v1/window/close")
	if n := sub.Pending(); n != 0 {
		t.Errorf("a window action announced itself (%d events)", n)
	}
}
