package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/handlers"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

type recordingReaderNotifier struct {
	types []string
	data  []map[string]any
}

func (n *recordingReaderNotifier) Publish(typ string, data any) {
	n.types = append(n.types, typ)
	if m, ok := data.(map[string]any); ok {
		n.data = append(n.data, m)
	}
}

func readerAt(hour int) time.Time {
	return time.Date(2026, 11, 7, hour, 0, 0, 123_000_000, time.UTC)
}

// TestReaderGetReturnsTheSharedWatermark: a resync reads the record without acknowledging it.
func TestReaderGetReturnsTheSharedWatermark(t *testing.T) {
	h := handlers.ReaderHandler{
		State: func(context.Context) (entities.ReaderState, error) {
			return entities.ReaderState{SeenAt: readerAt(8)}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/reader", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		SeenAt string `json:"seen_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SeenAt != readerAt(8).Format(time.RFC3339Nano) {
		t.Fatalf("seen_at = %q", body.SeenAt)
	}
}

// TestReaderActivateReturnsTheBaselineAndAnnouncesTheMove: the client gets the previous watermark to
// mark from, and every other window gets one `reader` frame with the new one.
func TestReaderActivateReturnsTheBaselineAndAnnouncesTheMove(t *testing.T) {
	notifier := &recordingReaderNotifier{}
	h := handlers.ReaderHandler{
		Activator: func(context.Context) (entities.ReaderActivation, error) {
			return entities.ReaderActivation{Since: readerAt(6), SeenAt: readerAt(9), Changed: true}, nil
		},
		Notifier: notifier,
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Since  string `json:"since"`
		SeenAt string `json:"seen_at"`
		First  bool   `json:"first"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Since != readerAt(6).Format(time.RFC3339Nano) || body.SeenAt != readerAt(9).Format(time.RFC3339Nano) {
		t.Fatalf("since/seen = %q/%q", body.Since, body.SeenAt)
	}
	if body.First {
		t.Fatal("first = true, want false")
	}
	if len(notifier.types) != 1 || notifier.types[0] != "reader" {
		t.Fatalf("events = %v, want one reader", notifier.types)
	}
	if notifier.data[0]["seen_at"] != readerAt(9).Format(time.RFC3339Nano) {
		t.Fatalf("event = %v", notifier.data[0])
	}
}

// TestReaderActivateFirstOmitsTheBaseline: a fresh vault marks nothing, so the wire says so by
// leaving `since` out instead of sending a zero instant.
func TestReaderActivateFirstOmitsTheBaseline(t *testing.T) {
	h := handlers.ReaderHandler{
		Activator: func(context.Context) (entities.ReaderActivation, error) {
			return entities.ReaderActivation{SeenAt: readerAt(8), First: true, Changed: true}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active", nil))

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["since"]; ok {
		t.Fatalf("since present on the first activation: %v", raw)
	}
	if raw["first"] != true {
		t.Fatalf("first = %v, want true", raw["first"])
	}
}

// TestReaderActivateWithoutAChangeStaysQuiet: an activation with nothing new must not reload every
// other window.
func TestReaderActivateWithoutAChangeStaysQuiet(t *testing.T) {
	notifier := &recordingReaderNotifier{}
	h := handlers.ReaderHandler{
		Activator: func(context.Context) (entities.ReaderActivation, error) {
			return entities.ReaderActivation{Since: readerAt(9), SeenAt: readerAt(9)}, nil
		},
		Notifier: notifier,
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active", nil))
	if len(notifier.types) != 0 {
		t.Fatalf("events = %v, want none", notifier.types)
	}
}
