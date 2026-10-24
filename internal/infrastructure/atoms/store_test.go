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
	// A leftover from an older build goes when it is asked to.
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Atoms\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone, err := s.RemoveStaleIndex()
	if err != nil || !gone {
		t.Fatalf("remove stale index: gone=%v err=%v", gone, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); !os.IsNotExist(err) {
		t.Errorf("the stale index survived: %v", err)
	}
	if gone, err := s.RemoveStaleIndex(); err != nil || gone {
		t.Errorf("a second call must be a no-op: gone=%v err=%v", gone, err)
	}
}

// A vault written before the ID layout keeps its claims in branch folders, each with a generated
// index beside them; the reorganize moves the claims to their canonical path, deletes the generated
// indexes and removes the folders — including one whose claims are all gone and which holds nothing
// but its index.
func TestReorganizeMovesClaimsOutOfBranchFolders(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New(dir)

	a := sampleAtom("Caching is only safe")
	old := filepath.Join(dir, "caching")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + a.ID.String() + "\nclaim: " + a.Claim + "\ntopic: caching\n---\n\nthe body\n"
	if err := os.WriteFile(filepath.Join(old, "caching-is-only-safe.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "index.md"), []byte("# caching\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A branch whose claims are all gone keeps nothing but its generated index.
	empty := filepath.Join(dir, "emptybranch")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "index.md"), []byte("# emptybranch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A shard folder holds directories, never files: it is never the old layout.
	if err := os.MkdirAll(filepath.Join(dir, "7f", "3a"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A claim lying directly in atoms/ instead of its shard is moved like one in a branch folder:
	// every claim ends at its canonical path, wherever it was found.
	stray := sampleAtom("A claim that lies in the atoms root")
	strayBody := "---\nid: " + stray.ID.String() + "\nclaim: " + stray.Claim + "\ntopic: caching\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, stray.ID.String()+".md"), []byte(strayBody), 0o644); err != nil {
		t.Fatal(err)
	}

	folders, claims, indexes, err := s.DetectLegacyFolders()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(folders) != 2 || folders[0] != "caching" || folders[1] != "emptybranch" || claims != 2 || indexes != 2 {
		t.Fatalf("detect = %v/%d claims/%d indexes, want [caching emptybranch]/2/2", folders, claims, indexes)
	}

	moved, removed, indexesGone, skipped, kept, err := s.Reorganize(ctx)
	if err != nil {
		t.Fatalf("reorganize: %v", err)
	}
	if len(moved) != 2 || removed != 2 || indexesGone != 2 || skipped != 0 || len(kept) != 0 {
		t.Fatalf("moved=%d removed=%d indexes=%d skipped=%d kept=%v", len(moved), removed, indexesGone, skipped, kept)
	}
	// The moved atoms come back with their new path: the index has to learn it, or the next write
	// recreates the folder this move removed.
	byID := map[ulid.ULID]string{}
	for _, m := range moved {
		byID[m.ID] = m.Path
	}
	if byID[a.ID] != Path(a.ID) || byID[stray.ID] != Path(stray.ID) {
		t.Errorf("moved atoms = %v, want %q and %q", byID, Path(a.ID), Path(stray.ID))
	}
	for _, gone := range []string{"caching", "emptybranch"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("the branch folder %s survived: %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(Path(a.ID)))); err != nil {
		t.Errorf("the atom is not at its canonical path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(Path(stray.ID)))); err != nil {
		t.Errorf("the stray claim is not at its canonical path: %v", err)
	}

	// Idempotent: a second pass finds nothing.
	if moved, removed, _, _, _, err := s.Reorganize(ctx); err != nil || len(moved) != 0 || removed != 0 {
		t.Errorf("second pass: moved=%d removed=%d err=%v", len(moved), removed, err)
	}
	if names, _, _, _ := s.DetectLegacyFolders(); len(names) != 0 {
		t.Errorf("detect still reports folders: %v", names)
	}

	// And the scan still sees exactly the two claims, each at its canonical path.
	atoms, err := s.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(atoms) != 2 {
		t.Fatalf("scan = %+v, want 2 claims", atoms)
	}
	for _, got := range atoms {
		if got.Path != Path(got.ID) || got.Topic != "caching" {
			t.Errorf("scan: claim %s is not at its canonical path: %+v", got.ID, got)
		}
	}
}
