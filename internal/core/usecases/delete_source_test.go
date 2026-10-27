package usecases_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	atomstore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcelog"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
	treestore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/tree"
)

// noVaultWalkAtomStore fails the test if a delete ever walks atoms/ again. The walk reads and decodes
// every claim file; on a 172k-claim vault it cost 8–30 seconds per deleted source, which is what made
// one wiki save take 73 seconds for three removed links.
type noVaultWalkAtomStore struct {
	interfaces.AtomStore
	scans int
}

func (s *noVaultWalkAtomStore) Scan(context.Context) ([]entities.Atom, error) {
	s.scans++
	return nil, errors.New("the delete must answer from the index, not from a vault walk")
}

// TestDeletingASourceDeletesItsWholeStory: a source is ONE folder entry with everything about it —
// the object and its sidecars (rendering, step record, outline, log) — and deleting the source
// deletes that story. Nothing about a source that no longer exists may linger next to where it was,
// or it would look like knowledge.
func TestDeletingASourceDeletesItsWholeStory(t *testing.T) {
	ctx := context.Background()
	sourcesDir := t.TempDir()
	store := objects.New(sourcesDir)
	idx := newFakeIndex()

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	hash := src.Hash

	// The outline (step 2), the step record (steps 1..3) and the log: all next to the source.
	trees := treestore.New(sourcesDir)
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})
	if _, err := (usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).
		Execute(ctx, hash); err != nil {
		t.Fatalf("outline: %v", err)
	}
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), idx, nil)
	if err := steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{State: entities.StepOK}); err != nil {
		t.Fatalf("step record: %v", err)
	}
	log := sourcelog.New(sourcesDir)
	log.Append(ctx, hash, interfaces.SourceLogEvent{At: fixedClock{}.Now(), Phase: "capture", Level: "info", Message: "stored"})
	if err := store.PutRendered(hash, entities.SidecarRender, []byte("# rendering")); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	// The rendering's FIRST name, as a vault from before the rename carries it: a delete takes that
	// with it too. (The rename in between used a plain ".md", which for THIS source is the object
	// itself — that case is the store's own test: RemoveSidecars never removes the object.)
	if err := store.PutRendered(hash, entities.SidecarRenderLegacy, []byte("# old rendering")); err != nil {
		t.Fatalf("legacy rendering: %v", err)
	}

	// Everything is there before the deletion.
	for _, suffix := range []string{entities.SidecarRender, entities.SidecarRenderLegacy, entities.SidecarMeta, entities.SidecarOutline, entities.SidecarLog} {
		if _, err := os.Stat(objects.SidecarPath(sourcesDir, hash, suffix)); err != nil {
			t.Fatalf("%s is missing before the deletion: %v", suffix, err)
		}
	}

	del := usecases.DeleteSource{
		Store: store, Index: idx, Atoms: atomstore.New(t.TempDir()), AtomRepo: &fakeAtomRepo{},
		SourceLog: log, Steps: steps, Clock: fixedClock{},
	}
	if _, err := del.Execute(ctx, hash); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// The object is gone from the store (it was moved to the trash) …
	if _, err := os.Stat(filepath.Join(sourcesDir, hash[:2], hash[2:4], hash+".md")); !os.IsNotExist(err) {
		t.Error("the object survived the deletion")
	}
	// … and no sidecar of this hash is left anywhere in the vault's sources/.
	left := []string{}
	_ = filepath.WalkDir(sourcesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.Contains(d.Name(), hash) {
			left = append(left, path)
		}
		return nil
	})
	if len(left) != 0 {
		t.Errorf("the source left files behind: %v", left)
	}
}

// TestDeleteSourceDoesNotWalkTheAtomVault: the atom lookup is the INDEX's answer. The vault walk is
// O(vault) and ran once per deleted source — a sitemap reconciling 30 pages paid it 30 times, and a
// wiki save that dropped three links paid it three times inside the request.
func TestDeleteSourceDoesNotWalkTheAtomVault(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	hash := strings.Repeat("a", 64)
	if err := idx.UpsertSource(ctx, entities.Source{
		Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: "aa/aa/" + hash + ".md", Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	spy := &noVaultWalkAtomStore{AtomStore: atomstore.New(t.TempDir())}
	del := usecases.DeleteSource{
		Store: store, Index: idx, Atoms: spy, AtomRepo: &fakeAtomRepo{}, Clock: fixedClock{},
	}
	if _, err := del.Execute(ctx, hash); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if spy.scans != 0 {
		t.Fatalf("the delete walked atoms/ %d time(s); it must answer from the index", spy.scans)
	}
	if _, err := idx.GetSourceByHash(ctx, hash); err == nil {
		t.Fatal("the source row survived the deletion")
	}
}
