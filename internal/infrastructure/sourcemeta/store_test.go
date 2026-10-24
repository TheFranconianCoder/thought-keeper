package sourcemeta_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
)

const hash = "7f3a1c2b4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8"

// TestStepRecordLivesNextToTheSource: the record is a file in the vault
// (sources/<ab>/<cd>/<hash>.meta), not a database row — what a document has been through has to
// survive a lost index.
func TestStepRecordLivesNextToTheSource(t *testing.T) {
	dir := t.TempDir()
	s := sourcemeta.New(dir)

	rec, err := s.Set(hash, entities.StepOutline, entities.StepStatus{
		State: entities.StepOK, At: time.Unix(1700000000, 0).UTC(), Detail: "512 nodes",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	want := filepath.Join(dir, "7f", "3a", hash+".meta")
	path := s.Path(hash)
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the record is not a file in the vault: %v", err)
	}

	// A second step merges instead of replacing: the steps accumulate.
	rec, err = s.Set(hash, entities.StepDistill, entities.StepStatus{State: entities.StepRunning})
	if err != nil {
		t.Fatalf("second set: %v", err)
	}
	if rec.Steps.State(entities.StepOutline) != entities.StepOK {
		t.Error("the outline state was lost by the second write")
	}
	if rec.Steps.State(entities.StepDistill) != entities.StepRunning {
		t.Error("the distill state was not recorded")
	}
	if rec.Steps.State(entities.StepCapture) != entities.StepPending {
		t.Error("an unrecorded step must stay pending")
	}

	// And it reads back, freshly from disk.
	again, err := sourcemeta.New(dir).Read(hash)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if again.Steps.State(entities.StepOutline) != entities.StepOK || again.Steps.Get(entities.StepOutline).Detail != "512 nodes" {
		t.Errorf("read back = %+v", again.Steps[entities.StepOutline])
	}
}

// TestMissingRecordIsPendingNotAnError: a vault written before the steps existed has no .meta files,
// and that is a state, not a failure.
func TestMissingRecordIsPendingNotAnError(t *testing.T) {
	s := sourcemeta.New(t.TempDir())
	rec, err := s.Read(hash)
	if err != nil {
		t.Fatalf("reading a missing record must not fail: %v", err)
	}
	if rec.Steps.State(entities.StepCapture) != entities.StepPending {
		t.Errorf("state = %q, want pending", rec.Steps.State(entities.StepCapture))
	}
}

// TestCorruptRecordIsTolerated: the record is derived bookkeeping, the source is not. A damaged
// sidecar must not take the vault down.
func TestCorruptRecordIsTolerated(t *testing.T) {
	dir := t.TempDir()
	s := sourcemeta.New(dir)
	if err := os.MkdirAll(filepath.Dir(s.Path(hash)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(hash), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Read(hash)
	if err != nil {
		t.Fatalf("a corrupt record must be reported as unrecorded, not as an error: %v", err)
	}
	if rec.Steps.State(entities.StepOutline) != entities.StepPending {
		t.Error("a corrupt record must read as pending")
	}
}

// TestScanRebuildsTheMirror: this is what makes the index disposable — every step record can be
// read back from the vault.
func TestScanRebuildsTheMirror(t *testing.T) {
	dir := t.TempDir()
	s := sourcemeta.New(dir)
	if _, err := s.Set(hash, entities.StepCapture, entities.StepStatus{State: entities.StepOK}); err != nil {
		t.Fatal(err)
	}
	other := "aa3a1c2b4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8"
	if _, err := s.Set(other, entities.StepDistill, entities.StepStatus{State: entities.StepFailed, Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	// Files that are NOT records (an object, a readable rendering, a temp file) stay invisible.
	if err := os.WriteFile(s.Path(hash)+".tmp", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hash+".pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := s.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("scanned %d records, want 2: %v", len(recs), recs)
	}
	if recs[hash].Steps.State(entities.StepCapture) != entities.StepOK {
		t.Error("the capture state did not survive the scan")
	}
	if recs[other].Steps.Get(entities.StepDistill).Error != "boom" {
		t.Error("the failure reason did not survive the scan")
	}
}

// TestConcurrentRecordWritesKeepTheRecordValid is the regression for a truncated sidecar: the temp file
// used to be `<hash>.meta.tmp`, so two writers for the same source shared it and the first rename
// published the other one's half-written bytes. A record that stops parsing reads as "never recorded"
// — and takes the source's identity with it.
func TestConcurrentRecordWritesKeepTheRecordValid(t *testing.T) {
	dir := t.TempDir()
	s := sourcemeta.New(dir)
	if _, err := s.Update(hash, func(rec *sourcemeta.Record) error {
		rec.Kind = "url"
		rec.URL = "https://example.com/a"
		rec.OriginalName = "https://example.com/a"
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	steps := []entities.Step{entities.StepCapture, entities.StepOutline, entities.StepDistill}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			step := steps[i%len(steps)]
			_, _ = s.Set(hash, step, entities.StepStatus{State: entities.StepOK, Detail: "x"})
		}(i)
	}
	for range 10 {
		wg.Go(func() {
			_, _ = s.Update(hash, func(rec *sourcemeta.Record) error {
				rec.Language = "en"
				return nil
			})
		})
	}
	wg.Wait()

	b, err := os.ReadFile(s.Path(hash))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rec sourcemeta.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("the record no longer parses (%v): %q", err, string(b))
	}
	if rec.Kind != "url" || rec.URL != "https://example.com/a" {
		t.Fatalf("the identity was lost: %+v", rec)
	}
	if rec.Language != "en" {
		t.Fatalf("language = %q, want the field another writer set", rec.Language)
	}
	if rec.Steps.State(entities.StepCapture) != entities.StepOK ||
		rec.Steps.State(entities.StepOutline) != entities.StepOK ||
		rec.Steps.State(entities.StepDistill) != entities.StepOK {
		t.Fatalf("a step write was lost: %v", rec.Steps)
	}
	if rec.Updated.IsZero() {
		t.Fatal("the record carries no timestamp: an empty record would read as a fresh one")
	}
}
