package vocabulary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestVocabularyRoundTrip: the file is the truth, so what is written must be what is read back — the
// canonical names (grouped for a reader, flat for the parser) and, above all, what they ABSORBED. That
// mapping is the alias table: without it the next curation asks the same question again, and a
// write with the old name starts a new name.
func TestVocabularyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	run := ulid.Make()
	in := entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Canonical: []string{
			"software-engineering/caching",
			"software-engineering/testing",
			"machine-learning/transformers",
		},
		Absorbed: map[string]string{
			"cache-invalidation":   "software-engineering/caching",
			"Caching Strategies":   "software-engineering/caching",
			"machine-learning/nlp": "machine-learning/transformers",
		},
		BroadTokens: []string{"go", "rust", "benchmark"},
		Decided:     []string{"go", "caching", "software-engineering/testing"},
		Updated:     time.UnixMilli(1_700_000_000_000).UTC(),
		Run:         run,
	}
	if err := s.Write(in); err != nil {
		t.Fatalf("write: %v", err)
	}

	// It reads like a vocabulary: the canonical names grouped under their domain.
	b, err := os.ReadFile(filepath.Join(dir, "topics.md"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	for _, want := range []string{"## software-engineering", "## machine-learning", "# Absorbed"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the file does not read as a vocabulary (%q missing):\n%s", want, b)
		}
	}

	got, err := s.Read(entities.VocabularyTopics)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Canonical) != len(in.Canonical) {
		t.Errorf("canonical = %v, want %v", got.Canonical, in.Canonical)
	}
	for _, c := range in.Canonical {
		if !got.Known(c) {
			t.Errorf("%q is not canonical after the round trip", c)
		}
	}
	if got.Resolve("cache-invalidation") != "software-engineering/caching" {
		t.Errorf("an absorbed name did not resolve: %v", got.Absorbed)
	}
	if got.Resolve("something-new") != "something-new" {
		t.Errorf("an unknown name must be itself, got %q", got.Resolve("something-new"))
	}
	if got.Known("something-new") {
		t.Error("an unknown name is not known")
	}
	if !got.BroadTokenSet()["go"] || !got.BroadTokenSet()["rust"] {
		t.Errorf("broad tokens did not survive the round trip: %v", got.BroadTokens)
	}
	// Decided is what stops the next pass from re-asking the same catalogue: losing it turned every
	// curation into a full-catalogue question (and a full-corpus heap spike) every cooldown.
	for _, d := range in.Decided {
		if !got.Judged(d) {
			t.Errorf("%q is not decided after the round trip", d)
		}
	}
	if got.Run != run {
		t.Errorf("run = %v, want %v", got.Run, run)
	}
	if !got.Updated.Equal(in.Updated) {
		t.Errorf("updated = %v, want %v", got.Updated, in.Updated)
	}
}

// TestVocabularyMissingFileIsEmpty: a vault whose names have never been curated has no file, and
// that is a normal state — every observed name is then "new", which is exactly what the report says.
func TestVocabularyMissingFileIsEmpty(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "not-there"))
	v, err := s.Read(entities.VocabularyTopics)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(v.Canonical) != 0 || len(v.Absorbed) != 0 {
		t.Errorf("a missing file must read as empty, got %+v", v)
	}
	if v.Known("anything") {
		t.Error("an empty vocabulary knows nothing")
	}
}
