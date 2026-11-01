package atoms

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func sampleAtom(claim string) entities.Atom {
	return entities.Atom{
		ID:     ulid.Make(),
		Topic:  "Caching",
		Claim:  claim,
		Body:   "body",
		Type:   entities.AtomClaim,
		Status: entities.AtomActive,
	}
}

// The atom is addressed by its ID: two claims with the SAME text are two files, and writing one again
// lands on the same file — one id, one file, as one hash is one object.
func TestWriteReadByID(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())

	a := sampleAtom("Caching is only safe")
	stored, err := s.Write(ctx, a)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if stored.Path != Path(a.ID) {
		t.Errorf("path = %q, want %q", stored.Path, Path(a.ID))
	}
	if !strings.HasSuffix(stored.Path, a.ID.String()+".md") {
		t.Errorf("path = %q, want the id as the file name", stored.Path)
	}
	if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(stored.Path))); err != nil {
		t.Fatalf("file: %v", err)
	}

	// A different atom with the same claim is a different file (the ID decides, not the text).
	b := sampleAtom("Caching is only safe")
	storedB, err := s.Write(ctx, b)
	if err != nil {
		t.Fatalf("write b: %v", err)
	}
	if storedB.Path == stored.Path {
		t.Fatalf("two atoms share a path: %q", stored.Path)
	}

	got, err := s.Read(ctx, a.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != a.ID || got.Claim != a.Claim {
		t.Errorf("read = %+v, want %s", got, a.ID)
	}

	// Rewriting the same atom id reuses the same path.
	stored2, err := s.Write(ctx, a)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if stored2.Path != stored.Path {
		t.Errorf("rewrite path = %q, want %q", stored2.Path, stored.Path)
	}
}

// The shard comes from the RANDOM part of the ULID: two claims of one run must not land in one
// folder (the first ten characters of a ULID are a timestamp).
func TestShardSpreadsOneRun(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		seen[Shard(ulid.Make())] = true
	}
	if len(seen) < 100 {
		t.Errorf("200 ids produced only %d shards", len(seen))
	}
}

// TestScanReportsAnUnreadableClaimFile: a rebuild must be able to tell "this claim is not there"
// from "I could not read it". The file is returned, never skipped — the reindex refuses to run.
func TestScanReportsAnUnreadableClaimFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.Write(ctx, sampleAtom("Caching is only safe")); err != nil {
		t.Fatalf("write: %v", err)
	}
	broken := filepath.Join(dir, "broken.md")
	if err := os.WriteFile(broken, []byte("not an atom at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "no-id.md"), []byte("---\nclaim: no id\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	atoms, unreadable, err := s.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(atoms) != 1 {
		t.Fatalf("scan = %d atom(s), want the one readable claim", len(atoms))
	}
	if len(unreadable) != 2 {
		t.Fatalf("unreadable = %v, want the broken file and the file without an id", unreadable)
	}
}

// The atoms directory holds claims and nothing else: no generated index. The branch catalog is
// SQLite's job (list_branches, search_atoms), and a second catalog would be one more thing to keep in
// step with it.
func TestStoreWritesNoIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.Write(ctx, sampleAtom("Caching is only safe")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); !os.IsNotExist(err) {
		t.Errorf("an index was written: %v", err)
	}
}
