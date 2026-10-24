package usecases_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
)

// TestDistillTwoAtomsSameBranch reproduces the "UNIQUE constraint failed:
// atoms.branch, atoms.slug" bug: two atoms in the same branch must both persist,
// which requires the atom store's final slug to reach the index.
func TestDistillTwoAtomsSameBranch(t *testing.T) {
	ctx := context.Background()

	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	atomRepo := sqlc.NewAtomRepository(db)

	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "n.md", Reader: strings.NewReader("# X\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).Execute(ctx, src.Hash); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	// Distill always runs with the source already in the index (it loads it by hash), and
	// provenance is required: an unknown source hash is an error, not a silent skip.
	if err := sqlc.NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: src.Hash + ".md", Title: "X", Bytes: int64(len("# X\nbody\n")),
		TreeState: entities.TreeBuilt, ImportedAt: fixedClock{}.Now(), Meta: "{}",
	}); err != nil {
		t.Fatalf("index source: %v", err)
	}

	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		claimsResponse(
			`{"topic":"x","claim":"Alpha claim one","body":"b","nodes":["0.1"]}`,
			`{"topic":"x","claim":"Beta claim two","body":"b","nodes":["0.1"]}`,
		),
		completeResponse(),
	}}

	az := usecases.DistillSource{
		Index: idx, Trees: trees, Atoms: atoms.New(t.TempDir()), AtomRepo: atomRepo,
		Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}
	if _, err := az.Distill(ctx, src.Hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil)); err != nil {
		t.Fatalf("distill: %v", err)
	}

	got, err := atomRepo.ListAtoms(ctx, "x", 10, 0)
	if err != nil {
		t.Fatalf("list atoms: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("atoms = %d, want 2 (same branch, one claim each)", len(got))
	}
	for _, a := range got {
		if a.Path == "" || !strings.HasSuffix(a.Path, a.ID.String()+".md") {
			t.Errorf("atom %s is not addressed by its id: path = %q", a.ID, a.Path)
		}
	}
}

// failingSaveRepo is a repository whose atom write always fails — the state a stale index leaves the
// app in (see the schema check in persistence.Open).
type failingSaveRepo struct {
	interfaces.AtomRepository
}

func (failingSaveRepo) SaveAtom(context.Context, entities.Atom) error {
	return errors.New("upsert atom: constraint failed: NOT NULL constraint failed: atoms.slug (1299)")
}

// TestFailedAtomWriteLeavesNoFile: the atom FILE is written first (it is the source of truth) and the
// index row second. A write that cannot be recorded must not leave the file behind — an atom the index
// never saw is an orphan, invisible to every page, every count and every cleanup, and a model that
// retries a failed write leaves one per attempt.
func TestFailedAtomWriteLeavesNoFile(t *testing.T) {
	ctx := context.Background()

	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	atomRepo := sqlc.NewAtomRepository(db)

	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "n.md", Reader: strings.NewReader("# X\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).Execute(ctx, src.Hash); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	if err := sqlc.NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: src.Hash + ".md", Title: "X", Bytes: int64(len("# X\nbody\n")),
		TreeState: entities.TreeBuilt, ImportedAt: fixedClock{}.Now(), Meta: "{}",
	}); err != nil {
		t.Fatalf("index source: %v", err)
	}

	atomsDir := t.TempDir()
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		claimsResponse(
			`{"topic":"x","claim":"Alpha claim one","body":"b","nodes":["0.1"]}`,
			`{"topic":"x","claim":"Beta claim two","body":"b","nodes":["0.1"]}`,
		),
		completeResponse(),
	}}

	az := usecases.DistillSource{
		Index: idx, Trees: trees, Atoms: atoms.New(atomsDir), AtomRepo: failingSaveRepo{atomRepo},
		Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}
	// The run reports its own failure (a run that writes nothing while tool calls failed ends with the
	// first error); what matters here is the vault, not the error text.
	_, _ = az.Distill(ctx, src.Hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil))

	left := 0
	err = filepath.WalkDir(atomsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == "index.md" {
			return nil
		}
		left++
		t.Logf("left behind: %s", path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if left != 0 {
		t.Errorf("%d file(s) left behind by a failed write, want 0", left)
	}
}
