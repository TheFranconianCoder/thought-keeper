package reader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestStoreRoundTripsBothWatermarks keeps the sub-second part: a watermark is a source's activity
// instant, and rounding it would make a source in the same second look older than it is.
func TestStoreRoundTripsBothWatermarks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reader")
	store := New(dir)
	start := time.Date(2026, 11, 7, 8, 0, 0, 123_000_000, time.UTC)
	sources := time.Date(2026, 11, 8, 9, 0, 0, 456_000_000, time.UTC)

	if err := store.Write(context.Background(), entities.ReaderState{StartSeenAt: start, SourcesSeenAt: sources}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, File)); statErr != nil {
		t.Fatalf("record not written: %v", statErr)
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.Equal(start) || !got.SourcesSeenAt.Equal(sources) {
		t.Fatalf("read = %v/%v, want %v/%v", got.StartSeenAt, got.SourcesSeenAt, start, sources)
	}
}

// TestStoreWritesOnlyTheAcknowledgedScope: the first Start visit must not speak for Sources. The
// omitted scope stays zero and is initialized by its own first visit.
func TestStoreWritesOnlyTheAcknowledgedScope(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)

	if err := store.Write(context.Background(), entities.ReaderState{StartSeenAt: start}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.Equal(start) || !got.SourcesSeenAt.IsZero() {
		t.Fatalf("read = %v/%v, want %v/zero", got.StartSeenAt, got.SourcesSeenAt, start)
	}
}

// TestStoreSeedsBothScopesFromALegacyRecord: a record written before the two watermarks existed
// carries one instant for both lenses; the next acknowledgment replaces only its own.
func TestStoreSeedsBothScopesFromALegacyRecord(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	if err := os.WriteFile(store.Path(), []byte("---\nseen_at: "+legacy.Format(time.RFC3339Nano)+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.Equal(legacy) || !got.SourcesSeenAt.Equal(legacy) {
		t.Fatalf("read = %v/%v, want both %v", got.StartSeenAt, got.SourcesSeenAt, legacy)
	}
}

// TestStoreTreatsAMissingRecordAsAFreshVault: no client has opened the vault yet, which is not an
// error and must not initialize a watermark by itself.
func TestStoreTreatsAMissingRecordAsAFreshVault(t *testing.T) {
	got, err := New(filepath.Join(t.TempDir(), "reader")).Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.IsZero() || !got.SourcesSeenAt.IsZero() {
		t.Fatalf("read = %v/%v, want zero/zero", got.StartSeenAt, got.SourcesSeenAt)
	}
}

// TestStoreTreatsAMalformedRecordAsAbsent: one damaged line of app state must not hide the vault.
func TestStoreTreatsAMalformedRecordAsAbsent(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("---\nstart_seen_at: not-a-time\nsources_seen_at: also-bad\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.IsZero() || !got.SourcesSeenAt.IsZero() {
		t.Fatalf("read = %v/%v, want zero/zero", got.StartSeenAt, got.SourcesSeenAt)
	}
}

// TestStoreRefusesAnEmptyState keeps an acknowledgment from inventing an instant.
func TestStoreRefusesAnEmptyState(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	if err := store.Write(context.Background(), entities.ReaderState{}); err == nil {
		t.Fatal("write of an empty state succeeded")
	}
}
