package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

// SSEHandler streams hub events to the PWA over Server-Sent Events.
type SSEHandler struct {
	Hub *stream.Hub
}

// Stream serves GET /api/v1/events. It holds the connection open and pushes one
// `data: {json}` line per event, plus a comment heartbeat so proxies keep it alive.
func (h SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	sub := h.Hub.Subscribe()
	defer h.Hub.Unsubscribe(sub)

	// One reader goroutine drains the subscriber; the loop below only writes and pings, so a
	// slow client can neither make the hub wait nor starve the heartbeat.
	msgs := make(chan []byte, 1)
	go func() {
		defer close(msgs)
		for {
			msg, ok := sub.Next(r.Context())
			if !ok {
				return
			}
			select {
			case msgs <- msg:
			case <-r.Context().Done():
				return
			}
		}
	}()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg, open := <-msgs:
			if !open {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
