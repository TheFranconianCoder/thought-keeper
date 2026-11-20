package reader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stored reports whether the record carries a top-level key. It reads the file, not the store, so a
// write the reader never asked for is visible.
func stored(t *testing.T, store *Store, key string) bool {
	t.Helper()
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, key+":") {
			return true
		}
	}
	return false
}

// writeRecord puts a raw record on disk, as an older version of the app left it.
func writeRecord(t *testing.T, store *Store, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("---\n"+body+"---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStoreRoundTripsTheWatermark keeps the sub-second part: a watermark is a source's activity
// instant, and rounding it would make a source in the same second look older than it is.
func TestStoreRoundTripsTheWatermark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reader")
	store := New(dir)
	seen := time.Date(2026, 11, 8, 9, 0, 0, 456_000_000, time.UTC)

	if err := store.WriteSeen(context.Background(), seen); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, File)); statErr != nil {
		t.Fatalf("record not written: %v", statErr)
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.SourcesSeenAt.Equal(seen) {
		t.Fatalf("read = %v, want %v", got.SourcesSeenAt, seen)
	}
	if !stored(t, store, "sources_seen_at") {
		t.Fatal("the record does not carry the watermark")
	}
}

// TestStoreReadsARecordFromTheSplitEra: a record that still holds both watermarks answers with the
// Sources one — it is the batch's baseline, and the front door summarizes the same batch.
func TestStoreReadsARecordFromTheSplitEra(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	sources := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	writeRecord(t, store, "start_seen_at: 2026-11-09T10:00:00Z\nsources_seen_at: "+
		sources.Format(time.RFC3339Nano)+"\n")

	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.SourcesSeenAt.Equal(sources) {
		t.Fatalf("read = %v, want the sources watermark %v", got.SourcesSeenAt, sources)
	}
}

// TestStoreReadsALegacyRecord: a record with the single old key still answers the batch's baseline.
func TestStoreReadsALegacyRecord(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	legacy := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	writeRecord(t, store, "seen_at: "+legacy.Format(time.RFC3339Nano)+"\n")

	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.SourcesSeenAt.Equal(legacy) {
		t.Fatalf("read = %v, want the legacy instant %v", got.SourcesSeenAt, legacy)
	}
}

// TestStoreDropsTheRetiredKeysOnWrite: one watermark, one key. The legacy line and the retired
// start_seen_at line must not outlive the acknowledgment that replaces them.
func TestStoreDropsTheRetiredKeysOnWrite(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	writeRecord(t, store, "seen_at: 2026-11-07T08:00:00Z\nstart_seen_at: 2026-11-09T10:00:00Z\n")

	seen := time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC)
	if err := store.WriteSeen(context.Background(), seen); err != nil {
		t.Fatalf("write: %v", err)
	}
	if stored(t, store, "seen_at") {
		t.Fatal("the legacy instant outlived the write")
	}
	if stored(t, store, "start_seen_at") {
		t.Fatal("the retired start watermark outlived the write")
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.SourcesSeenAt.Equal(seen) {
		t.Fatalf("read = %v, want %v", got.SourcesSeenAt, seen)
	}
}

// TestStoreTreatsAMissingRecordAsAFreshVault: no client has opened the vault yet, which is not an
// error and must not initialize a watermark by itself.
func TestStoreTreatsAMissingRecordAsAFreshVault(t *testing.T) {
	got, err := New(filepath.Join(t.TempDir(), "reader")).Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.SourcesSeenAt.IsZero() {
		t.Fatalf("read = %v, want zero", got.SourcesSeenAt)
	}
}

// TestStoreTreatsAMalformedRecordAsAbsent: one damaged line of app state must not hide the vault.
func TestStoreTreatsAMalformedRecordAsAbsent(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	writeRecord(t, store, "sources_seen_at: also-bad\nseen_at: not-a-time\n")

	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.SourcesSeenAt.IsZero() {
		t.Fatalf("read = %v, want zero", got.SourcesSeenAt)
	}
}

// TestStoreRefusesAnEmptyWrite keeps an acknowledgment from inventing an instant.
func TestStoreRefusesAnEmptyWrite(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	if err := store.WriteSeen(context.Background(), time.Time{}); err == nil {
		t.Fatal("write of an empty instant succeeded")
	}
}
