package objects

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestPutDedupesAndScans(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())

	first, err := s.Put(ctx, ".md", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(first.Hash) != 64 {
		t.Fatalf("hash = %q, want 64 hex chars", first.Hash)
	}
	if first.Bytes != int64(len("hello world")) {
		t.Errorf("bytes = %d, want %d", first.Bytes, len("hello world"))
	}

	// Identical bytes deduplicate to the same object.
	second, err := s.Put(ctx, ".md", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if first.Hash != second.Hash || first.Path != second.Path {
		t.Errorf("dedupe failed: %+v vs %+v", first, second)
	}

	objs, err := s.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Scan returned %d objects, want 1", len(objs))
	}

	rc, err := s.Open(first.Hash, ".md")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello world" {
		t.Errorf("Open = %q, want hello world", got)
	}
}

func TestPutRejectsMissingExtension(t *testing.T) {
	if _, err := New(t.TempDir()).Put(context.Background(), "", strings.NewReader("x")); err == nil {
		t.Fatal("expected error for missing extension")
	}
}

func TestScanIgnoresTmp(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	if _, err := s.Put(ctx, ".txt", strings.NewReader("a")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	objs, err := s.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Scan returned %d objects, want 1", len(objs))
	}
}

// TestDerivedSidecarsAreNotObjects: a source's derived files live next to the object — its readable
// rendering (<hash>.md) and its step record (<hash>.meta). They are NOT objects: a scan that picked
// them up would turn one PDF into a second, phantom source of kind md.
func TestDerivedSidecarsAreNotObjects(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	obj, err := store.Put(context.Background(), ".pdf", strings.NewReader("%PDF-1.7 the real source"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.PutRendered(obj.Hash, entities.SidecarRender, []byte("# the extracted text\n")); err != nil {
		t.Fatalf("put rendering: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, obj.Hash[:2], obj.Hash[2:4], obj.Hash+".meta"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.PutRendered(obj.Hash, entities.SidecarSummary, []byte("an overview\n")); err != nil {
		t.Fatalf("put summary: %v", err)
	}

	objs, err := store.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("scan returned %d objects, want only the PDF: %+v", len(objs), objs)
	}
	if objs[0].Hash != obj.Hash || objs[0].Ext != ".pdf" {
		t.Errorf("object = %+v, want the PDF", objs[0])
	}
}

// TestSidecarPathIsTheSameShardingAsTheObject: every derived file of a source sits where the object
// sits (sources/<ab>/<cd>/<hash><suffix>), because the rule lives in one function — the rendering,
// the step record, the outline and the log cannot drift apart.
func TestSidecarPathIsTheSameShardingAsTheObject(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	obj, err := store.Put(context.Background(), ".pdf", strings.NewReader("%PDF one source"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	objectPath := filepath.Join(dir, filepath.FromSlash(obj.Path))
	for _, suffix := range []string{entities.SidecarRender, entities.SidecarMeta, entities.SidecarOutline, entities.SidecarSummary, entities.SidecarLog} {
		side := SidecarPath(dir, obj.Hash, suffix)
		if filepath.Dir(side) != filepath.Dir(objectPath) {
			t.Errorf("%s lives in %s, the object in %s", suffix, filepath.Dir(side), filepath.Dir(objectPath))
		}
		if !strings.HasSuffix(side, obj.Hash+suffix) {
			t.Errorf("%s = %q, want the hash plus the suffix", suffix, side)
		}
	}
}

// TestRemoveSidecarsTakesTheWholeStory: deleting a source deletes everything that belongs to it —
// and nothing of a source that no longer exists may linger next to where it was.
func TestRemoveSidecarsTakesTheWholeStory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := New(dir)
	obj, err := store.Put(ctx, ".pdf", strings.NewReader("%PDF a source with a story"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.PutRendered(obj.Hash, entities.SidecarRender, []byte("# rendering")); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{entities.SidecarMeta, entities.SidecarOutline, entities.SidecarSummary, entities.SidecarLog, entities.SidecarLog + ".1"} {
		if err := os.WriteFile(SidecarPath(dir, obj.Hash, suffix), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.RemoveSidecars(obj.Hash, obj.Ext); err != nil {
		t.Fatalf("remove sidecars: %v", err)
	}
	for _, suffix := range []string{entities.SidecarRender, entities.SidecarMeta, entities.SidecarOutline, entities.SidecarSummary, entities.SidecarLog, entities.SidecarLog + ".1"} {
		if _, err := os.Stat(SidecarPath(dir, obj.Hash, suffix)); !os.IsNotExist(err) {
			t.Errorf("sidecar %s survived the deletion", suffix)
		}
	}
	// The object itself is the caller's business (it moves to the trash) — RemoveSidecars must not
	// have touched it.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(obj.Path))); err != nil {
		t.Errorf("the object was removed by RemoveSidecars: %v", err)
	}
	// Idempotent: a second call is not an error.
	if err := store.RemoveSidecars(obj.Hash, obj.Ext); err != nil {
		t.Errorf("second removal: %v", err)
	}
}

// TestAMarkdownNextToAnObjectIsTheRendering: the readable rendering is a plain <hash>.md — and a
// Markdown source's OBJECT is a plain <hash>.md too — the rendering's name is <hash>.capture.md now,
// which cannot be mistaken for an object, and a plain <hash>.md left over from before the rename is
// told apart by what sits beside it: one hash is one object (the store is content-addressed, the
// index keeps the hash unique), so a .md NEXT TO another object of the same hash was the rendering,
// and a .md alone is the source itself.
func TestAMarkdownNextToAnObjectIsTheRendering(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := New(dir)

	pdf, err := store.Put(ctx, ".pdf", strings.NewReader("%PDF a scanned report"))
	if err != nil {
		t.Fatalf("put pdf: %v", err)
	}
	if err := store.PutRendered(pdf.Hash, entities.SidecarRender, []byte("# the extracted text\n")); err != nil {
		t.Fatalf("put rendering: %v", err)
	}
	md, err := store.Put(ctx, ".md", strings.NewReader("# A real Markdown source\n"))
	if err != nil {
		t.Fatalf("put md: %v", err)
	}

	objs, err := store.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("scan returned %d objects, want the PDF and the Markdown source: %+v", len(objs), objs)
	}
	seen := map[string]string{}
	for _, o := range objs {
		seen[o.Hash] = o.Ext
	}
	if seen[pdf.Hash] != ".pdf" || seen[md.Hash] != ".md" {
		t.Errorf("objects = %+v, want the pdf and the md", seen)
	}
	if _, ok := seen[pdf.Hash]; !ok || len(seen) != 2 {
		t.Errorf("the rendering was scanned as a source: %+v", seen)
	}
}

// TestRemovingSidecarsKeepsTheObject: the rendering is a .md, and for a Markdown source that is the
// object itself — deleting a source must never delete its own bytes as a "sidecar" (the object goes
// to the trash, which is the caller's business).
func TestRemovingSidecarsKeepsTheObject(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := New(dir)
	src, err := store.Put(ctx, ".md", strings.NewReader("# A Markdown source\n"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.PutRendered(src.Hash, entities.SidecarMeta, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveSidecars(src.Hash, src.Ext); err != nil {
		t.Fatalf("remove sidecars: %v", err)
	}
	if _, ok := store.StatObject(src.Hash, src.Ext); !ok {
		t.Error("RemoveSidecars deleted the object itself")
	}
	if _, ok := store.StatSidecar(src.Hash, entities.SidecarMeta); ok {
		t.Error("the step record survived")
	}
}
