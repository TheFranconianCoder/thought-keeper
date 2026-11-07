package reader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStoreRoundTripsTheWatermark keeps the sub-second part: the watermark is a source's
// imported_at, and rounding it would make a source in the same second look older than it is.
func TestStoreRoundTripsTheWatermark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reader")
	store := New(dir)
	at := time.Date(2026, 11, 7, 8, 0, 0, 123_000_000, time.UTC)

	if err := store.Write(context.Background(), at); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, File)); statErr != nil {
		t.Fatalf("record not written: %v", statErr)
	}
	seen, ok, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok || !seen.Equal(at) {
		t.Fatalf("seen = %v, ok = %v, want %v", seen, ok, at)
	}
}

// TestStoreTreatsAMissingRecordAsAFreshVault: no client has opened the vault yet, which is not an
// error and must not initialize the watermark by itself.
func TestStoreTreatsAMissingRecordAsAFreshVault(t *testing.T) {
	seen, ok, err := New(filepath.Join(t.TempDir(), "reader")).Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if ok || !seen.IsZero() {
		t.Fatalf("seen = %v, ok = %v, want zero/false", seen, ok)
	}
}

// TestStoreTreatsAMalformedRecordAsAbsent: one damaged line of app state must not hide the vault.
func TestStoreTreatsAMalformedRecordAsAbsent(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("---\nseen_at: not-a-time\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen, ok, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if ok || !seen.IsZero() {
		t.Fatalf("seen = %v, ok = %v, want zero/false", seen, ok)
	}
}
