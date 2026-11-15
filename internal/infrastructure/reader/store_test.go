package reader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func mark(scope entities.ReaderScope, at time.Time) entities.ReaderWatermark {
	return entities.ReaderWatermark{Scope: scope, SeenAt: at}
}

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

// TestStoreRoundTripsBothWatermarks keeps the sub-second part: a watermark is a source's activity
// instant, and rounding it would make a source in the same second look older than it is.
func TestStoreRoundTripsBothWatermarks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reader")
	store := New(dir)
	start := time.Date(2026, 11, 7, 8, 0, 0, 123_000_000, time.UTC)
	sources := time.Date(2026, 11, 8, 9, 0, 0, 456_000_000, time.UTC)

	if err := store.WriteScopes(context.Background(), []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, start),
		mark(entities.ReaderScopeSources, sources),
	}); err != nil {
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

// TestStoreWritesOnlyTheGivenScopes: a Start visit must never write sources_seen_at. The scope the
// reader did not visit keeps its own value — here, none at all.
func TestStoreWritesOnlyTheGivenScopes(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)

	if err := store.WriteScopes(context.Background(), []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, start),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if stored(t, store, "sources_seen_at") {
		t.Fatal("a Start visit wrote the Sources scope")
	}
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.Equal(start) || !got.SourcesSeenAt.IsZero() {
		t.Fatalf("read = %v/%v, want %v/zero", got.StartSeenAt, got.SourcesSeenAt, start)
	}
}

// TestStoreKeepsTheScopeItWasNotGiven: a later visit to one lens leaves the other lens's stored
// instant exactly as it was.
func TestStoreKeepsTheScopeItWasNotGiven(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	ctx := context.Background()
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	sources := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	later := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

	if err := store.WriteScopes(ctx, []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, start), mark(entities.ReaderScopeSources, sources),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := store.WriteScopes(ctx, []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, later),
	}); err != nil {
		t.Fatalf("write start again: %v", err)
	}
	got, err := store.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.Equal(later) || !got.SourcesSeenAt.Equal(sources) {
		t.Fatalf("read = %v/%v, want %v/%v", got.StartSeenAt, got.SourcesSeenAt, later, sources)
	}
}

// TestStoreSeedsBothScopesFromALegacyRecord: a record written before the two watermarks existed
// carries one instant for both lenses; the next acknowledgment replaces only what it settles.
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

// TestStoreKeepsTheLegacyInstantUntilBothScopesAreWritten: the inherited baseline of the lens the
// reader has not visited yet must survive a one-scope write, and disappear once both are its own.
func TestStoreKeepsTheLegacyInstantUntilBothScopesAreWritten(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	ctx := context.Background()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	if err := os.WriteFile(store.Path(), []byte("---\nseen_at: "+legacy.Format(time.RFC3339Nano)+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

	if err := store.WriteScopes(ctx, []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, start),
	}); err != nil {
		t.Fatalf("write start: %v", err)
	}
	if !stored(t, store, "seen_at") {
		t.Fatal("the legacy instant was dropped before Sources had its own")
	}
	if stored(t, store, "sources_seen_at") {
		t.Fatal("a Start visit wrote the Sources scope")
	}
	got, err := store.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.StartSeenAt.Equal(start) || !got.SourcesSeenAt.Equal(legacy) {
		t.Fatalf("read = %v/%v, want %v/%v", got.StartSeenAt, got.SourcesSeenAt, start, legacy)
	}

	sources := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	if err := store.WriteScopes(ctx, []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, sources), mark(entities.ReaderScopeSources, sources),
	}); err != nil {
		t.Fatalf("write sources: %v", err)
	}
	if stored(t, store, "seen_at") {
		t.Fatal("the legacy instant outlived the second scope")
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

// TestStoreRefusesAnEmptyOrUnknownWrite keeps an acknowledgment from inventing an instant, and a
// typo from writing a watermark no lens owns.
func TestStoreRefusesAnEmptyOrUnknownWrite(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "reader"))
	at := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	if err := store.WriteScopes(context.Background(), nil); err == nil {
		t.Fatal("write of no watermark succeeded")
	}
	if err := store.WriteScopes(context.Background(), []entities.ReaderWatermark{
		mark(entities.ReaderScopeStart, time.Time{}),
	}); err == nil {
		t.Fatal("write of an empty instant succeeded")
	}
	if err := store.WriteScopes(context.Background(), []entities.ReaderWatermark{
		mark(entities.ReaderScope("wiki"), at),
	}); err == nil {
		t.Fatal("write of an unknown scope succeeded")
	}
}
