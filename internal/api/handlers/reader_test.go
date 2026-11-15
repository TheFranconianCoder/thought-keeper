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

// TestReaderGetReturnsBothWatermarks: a resync reads the record without acknowledging it.
func TestReaderGetReturnsBothWatermarks(t *testing.T) {
	h := handlers.ReaderHandler{
		State: func(context.Context) (entities.ReaderState, error) {
			return entities.ReaderState{StartSeenAt: readerAt(8), SourcesSeenAt: readerAt(9)}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/reader", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		StartSeenAt   string `json:"start_seen_at"`
		SourcesSeenAt string `json:"sources_seen_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.StartSeenAt != readerAt(8).Format(time.RFC3339Nano) {
		t.Fatalf("start_seen_at = %q", body.StartSeenAt)
	}
	if body.SourcesSeenAt != readerAt(9).Format(time.RFC3339Nano) {
		t.Fatalf("sources_seen_at = %q", body.SourcesSeenAt)
	}
}

// TestReaderActivateReturnsTheBaselineAndAnnouncesTheSettledScopes: the client gets its scope's
// previous watermark to mark from, the answer names every watermark the visit moved, and every other
// window gets one `reader` frame per moved scope — a Sources visit settles Start too.
func TestReaderActivateReturnsTheBaselineAndAnnouncesTheSettledScopes(t *testing.T) {
	notifier := &recordingReaderNotifier{}
	h := handlers.ReaderHandler{
		Activator: func(_ context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error) {
			if scope != entities.ReaderScopeSources {
				t.Fatalf("scope = %q, want sources", scope)
			}
			return entities.ReaderActivation{
				Scope: scope, Since: readerAt(6), SeenAt: readerAt(9),
				Settled: []entities.ReaderWatermark{
					{Scope: entities.ReaderScopeSources, SeenAt: readerAt(9)},
					{Scope: entities.ReaderScopeStart, SeenAt: readerAt(9)},
				},
			}, nil
		},
		Notifier: notifier,
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active?scope=sources", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Scope   string `json:"scope"`
		Since   string `json:"since"`
		SeenAt  string `json:"seen_at"`
		First   bool   `json:"first"`
		Settled []struct {
			Scope  string `json:"scope"`
			SeenAt string `json:"seen_at"`
		} `json:"settled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Scope != "sources" {
		t.Fatalf("scope = %q, want sources", body.Scope)
	}
	if body.Since != readerAt(6).Format(time.RFC3339Nano) || body.SeenAt != readerAt(9).Format(time.RFC3339Nano) {
		t.Fatalf("since/seen = %q/%q", body.Since, body.SeenAt)
	}
	if body.First {
		t.Fatal("first = true, want false")
	}
	if len(body.Settled) != 2 || body.Settled[0].Scope != "sources" || body.Settled[1].Scope != "start" {
		t.Fatalf("settled = %v, want sources then start", body.Settled)
	}
	if body.Settled[1].SeenAt != readerAt(9).Format(time.RFC3339Nano) {
		t.Fatalf("start seen_at = %q", body.Settled[1].SeenAt)
	}
	if len(notifier.types) != 2 || notifier.types[0] != "reader" || notifier.types[1] != "reader" {
		t.Fatalf("events = %v, want two reader frames", notifier.types)
	}
	if notifier.data[0]["scope"] != "sources" || notifier.data[0]["seen_at"] != readerAt(9).Format(time.RFC3339Nano) {
		t.Fatalf("first event = %v", notifier.data[0])
	}
	if notifier.data[1]["scope"] != "start" {
		t.Fatalf("second event = %v", notifier.data[1])
	}
	if _, ok := notifier.data[1]["first"]; ok {
		t.Fatalf("the other scope's frame claims a first visit: %v", notifier.data[1])
	}
}

// TestReaderActivateDefaultsToStart: a stale client that sends no scope acknowledges the front door,
// never the Sources marks.
func TestReaderActivateDefaultsToStart(t *testing.T) {
	h := handlers.ReaderHandler{
		Activator: func(_ context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error) {
			if scope != entities.ReaderScopeStart {
				t.Fatalf("scope = %q, want the default start", scope)
			}
			return entities.ReaderActivation{Scope: scope, Since: readerAt(9), SeenAt: readerAt(9)}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestReaderActivateRejectsAnUnknownScope keeps a typo from clearing the wrong watermark.
func TestReaderActivateRejectsAnUnknownScope(t *testing.T) {
	h := handlers.ReaderHandler{
		Activator: func(context.Context, entities.ReaderScope) (entities.ReaderActivation, error) {
			t.Fatal("activator called for an invalid scope")
			return entities.ReaderActivation{}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active?scope=wiki", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestReaderActivateFirstOmitsTheBaseline: a scope's first visit marks nothing, so the wire says so
// by leaving `since` out instead of sending a zero instant.
func TestReaderActivateFirstOmitsTheBaseline(t *testing.T) {
	h := handlers.ReaderHandler{
		Activator: func(_ context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error) {
			return entities.ReaderActivation{
				Scope: scope, SeenAt: readerAt(8), First: true,
				Settled: []entities.ReaderWatermark{{Scope: scope, SeenAt: readerAt(8)}},
			}, nil
		},
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active?scope=sources", nil))

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
		Activator: func(_ context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error) {
			return entities.ReaderActivation{Scope: scope, Since: readerAt(9), SeenAt: readerAt(9)}, nil
		},
		Notifier: notifier,
	}
	rec := httptest.NewRecorder()
	h.Activate(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/reader/active?scope=start", nil))
	if len(notifier.types) != 0 {
		t.Fatalf("events = %v, want none", notifier.types)
	}
}
