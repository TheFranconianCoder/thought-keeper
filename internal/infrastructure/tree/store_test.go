package tree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

const hash = "3dd5f7fe15e29619cd4072654cb9c5ec940ff527a864d758715591b59d5800db"

// TestOutlineLivesNextToTheSource: the outline is a source's own file
// (sources/<ab>/<cd>/<hash>.outline), not a central store. A source is ONE folder entry with
// everything about it — so deleting the source deletes its outline, and nothing about a source that
// no longer exists can linger somewhere else.
func TestOutlineLivesNextToTheSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New(dir)

	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		{NodeID: "0.1", Depth: 1, Title: "Chapter", TextStart: 40, TextEnd: 48, TextHash: "abc", HasText: true},
	}
	if err := s.Write(ctx, hash, nodes); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := filepath.Join(dir, "3d", "d5", hash+".outline")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the outline is not next to the source (%s): %v", want, err)
	}
	if got := s.path(hash); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}

	back, err := s.Read(ctx, hash)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(back) != 2 || back[1].TextStart != 40 || back[1].TextEnd != 48 || back[1].TextHash != "abc" {
		t.Errorf("read back = %+v", back)
	}
	// The outline REFERENCES the text, it does not copy it: the file must not contain the document.
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"text":`) {
		t.Errorf("the outline carries the node text: %s", raw)
	}

	// A node whose text could NOT be referenced (an extractor that normalised it) keeps its text: a
	// node nobody can point at must not lose its content.
	unreferenced := []entities.SourceNode{{NodeID: "0.1", Depth: 1, Title: "N", Text: "verbatim text"}}
	if err := s.Write(ctx, hash, unreferenced); err != nil {
		t.Fatal(err)
	}
	kept, err := s.Read(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Text != "verbatim text" {
		t.Errorf("unreferenced node = %+v, want its text kept", kept)
	}

	// An outline written before the references existed still carries the text: it is read, and its
	// hash derived from it, so a format change does not invalidate a derivation.
	legacy := `{"source_hash":"` + hash + `","nodes":[{"node_id":"0.1","title":"Old","text":"old text"}]}`
	if err := os.WriteFile(want, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := s.Read(ctx, hash)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if len(old) != 1 || old[0].Text != "old text" || old[0].TextHash == "" || !old[0].HasText {
		t.Errorf("legacy outline = %+v, want its text read and hashed", old)
	}

	// The outline is NOT an object: the store's scan must not turn it into a phantom source.
	objs, err := objects.New(dir).Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, o := range objs {
		if strings.Contains(o.Path, ".outline") {
			t.Errorf("the outline was scanned as an object: %+v", o)
		}
	}

	if err := s.Remove(ctx, hash); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Error("the outline survived its removal")
	}
	// Reading a missing outline is a not_found, not a crash: a source may simply not be outlined yet.
	if _, err := s.Read(ctx, hash); err == nil {
		t.Error("a missing outline must report not_found")
	}
}
