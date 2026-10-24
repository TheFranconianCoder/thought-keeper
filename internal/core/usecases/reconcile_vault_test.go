package usecases_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
	treestore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/tree"
)

// TestReconcileInvalidatesWhenAnArtefactIsGone is the job the vault needs to stay honest: the step
// record says the outline succeeded, the index says `built`, the lights are green — and the outline
// FILE is gone (deleted by accident, or by somebody tidying up). Everything derived from it is wrong
// (the atoms cite nodes of a tree that no longer exists, the pages describe a document that is no
// longer outlined), so the derivation is thrown away and the source is handed back to be derived
// again.
func TestReconcileInvalidatesWhenAnArtefactIsGone(t *testing.T) {
	ctx := context.Background()
	// Object and outline share the sources/ directory, as they do in a vault.
	sourcesDir := t.TempDir()
	store := objects.New(sourcesDir)
	idx := newFakeIndex()
	trees := treestore.New(sourcesDir)
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).
		Execute(ctx, src.Hash); err != nil {
		t.Fatalf("outline: %v", err)
	}
	_ = trees
	// The outline sidecar is written next to the source.
	outlinePath := objects.SidecarPath(sourcesDir, src.Hash, entities.SidecarOutline)

	// An atom with provenance and the page that cites it: the derivation this outline carries.
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
	atomRepo := &fakeAtomRepo{}
	atom := entities.Atom{
		ID: ulid.Make(), Topic: "caching", Claim: "Caching is subtle", Body: "b",
		Type: entities.AtomClaim, Status: entities.AtomActive,
		Sources:   []entities.AtomSource{{Hash: src.Hash, NodeID: "0.1"}},
		CreatedAt: fixedClock{}.Now(), UpdatedAt: fixedClock{}.Now(),
	}
	if _, err := atomStore.Write(ctx, atom); err != nil {
		t.Fatalf("atom: %v", err)
	}
	if err := atomRepo.SaveAtom(ctx, atom); err != nil {
		t.Fatalf("atom row: %v", err)
	}
	idx.m[src.Hash] = entities.Source{
		ID: src.ID, Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md",
		Title: "Caching", TreeState: entities.TreeBuilt,
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}).
			With(entities.StepOutline, entities.StepStatus{State: entities.StepOK}),
	}

	steps := &fakeSteps{}
	reconcile := usecases.ReconcileVault{
		Index: idx, Steps: steps, Store: store, SourceLog: &fakeSourceLog{},
		Clean: usecases.CleanSource{
			Parts: &fakeSections{}, Atoms: atomRepo, AtomStore: atomStore,
			Index: idx, Clock: fixedClock{},
		},
		Clock: fixedClock{},
	}

	// 1) Everything is there: nothing to repair.
	res, err := reconcile.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Invalidated) != 0 || len(res.Broken) != 0 {
		t.Fatalf("a healthy vault was repaired: %+v", res)
	}
	if res.Checked != 1 {
		t.Errorf("checked %d sources, want 1", res.Checked)
	}

	// 2) The outline file is gone.
	if err := os.Remove(outlinePath); err != nil {
		t.Fatalf("remove the outline: %v", err)
	}
	res, err = reconcile.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Invalidated) != 1 || res.Invalidated[0] != src.Hash {
		t.Fatalf("invalidated = %v, want the source whose outline is gone", res.Invalidated)
	}
	// The claims are gone: they describe a tree that does not exist. The cluster records that cited
	// them are a cache — the next cluster pass prunes the dead members.
	if len(atomRepo.saved) != 0 {
		t.Errorf("the claims survived: %+v", atomRepo.saved)
	}
	// The step record went back: capture survives (the source is readable), the outline does not.
	if len(steps.resets) != 1 || steps.resets[0] != src.Hash {
		t.Errorf("the step record was not reset: %v", steps.resets)
	}
	if got := idx.m[src.Hash].TreeState; got != entities.TreeNone {
		t.Errorf("tree_state = %q, want none so the outline is built again", got)
	}

	// 3) A source whose OBJECT is gone cannot be derived at all: it is reported, not silently green.
	idx.m[src.Hash] = entities.Source{ID: src.ID, Hash: "deadbeef", Kind: entities.SourceKindMD, Ext: ".md", Title: "x"}
	res, err = reconcile.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Broken) != 1 || res.Broken[0] != "deadbeef" {
		t.Errorf("broken = %v, want the source without an object", res.Broken)
	}
}

// TestReconcileWalksTheVaultInSlices: a huge vault is checked over several ticks, and every source is
// looked at — the pass reports where the next one starts and starts over at the end.
func TestReconcileWalksTheVaultInSlices(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	for i := range 3 {
		idx.m[string(rune('a'+i))] = entities.Source{Hash: string(rune('a' + i)), Kind: entities.SourceKindMD, Ext: ".md"}
	}
	r := usecases.ReconcileVault{Index: idx, Store: store, Limit: 2, Clock: fixedClock{}}

	first, err := r.Execute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Checked != 2 || first.Next != 2 {
		t.Errorf("first slice: checked=%d next=%d, want 2 and 2", first.Checked, first.Next)
	}
	r.Offset = first.Next
	second, err := r.Execute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Checked != 1 || second.Next != 0 {
		t.Errorf("second slice: checked=%d next=%d, want 1 and 0 (the walk ends and starts over)", second.Checked, second.Next)
	}
}

type fakeLanguage string

func (f fakeLanguage) Detect(string) string { return string(f) }

// TestReconcileBackfillsMissingLanguage: a vault written before sources carried a language hint
// enters the next consistency pass without one; the pass detects it and writes it into the record
// and the index.
func TestReconcileBackfillsMissingLanguage(t *testing.T) {
	ctx := context.Background()
	sourcesDir := t.TempDir()
	store := objects.New(sourcesDir)
	idx := newFakeIndex()
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), idx, nil)
	src, err := (usecases.IntakeSource{
		Store: store, Index: idx, Steps: steps, Clock: fixedClock{},
	}).Execute(ctx, usecases.IntakeInput{
		Name: "notes.md", Reader: strings.NewReader("# Caching\n\nCaching ist ein wichtiges Thema in der Softwareentwicklung.\n"),
	})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	reconcile := usecases.ReconcileVault{
		Index: idx, Steps: steps, Store: store, Clock: fixedClock{}, Languages: fakeLanguage("de"),
	}
	res, err := reconcile.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Languages != 1 {
		t.Fatalf("languages = %d, want one backfilled source", res.Languages)
	}
	if got := idx.m[src.Hash].Language; got != "de" {
		t.Fatalf("indexed language = %q, want de", got)
	}
	rec, err := steps.Steps(src.Hash)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if rec.State(entities.StepCapture) != entities.StepOK {
		t.Fatalf("capture = %q, want ok", rec.State(entities.StepCapture))
	}
}

// TestReconcileRestoresALostIdentity: a record can lose what the source IS (a truncated write) while
// the index still knows it. Without the repair a reindex would file a URL snapshot as Markdown and lose
// the address it came from — and the consistency job would keep invalidating a source it cannot check.
func TestReconcileRestoresALostIdentity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := sqlc.NewSourceRepository(db)
	metaStore := sourcemeta.New(sourcesDir)
	steps := sourcemeta.NewTracker(metaStore, index, nil)
	queue := usecases.QueueURLs{Index: index, Steps: steps, Clock: wikiClock{}}

	const link = "https://example.com/lost"
	hash := entities.URLHash(link)
	if _, err := queue.Execute(ctx, "page-1", []string{link}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	// The damage: the record is left with nothing but its address.
	if err := os.WriteFile(metaStore.Path(hash), []byte(`{"hash":"`+hash+`"}`+"\n"), 0o644); err != nil {
		t.Fatalf("damage: %v", err)
	}

	reconcile := usecases.ReconcileVault{Index: index, Steps: steps, Store: objects.New(sourcesDir), Clock: wikiClock{}}

	// The reading half reports the repair and writes nothing.
	dry := reconcile
	dry.DryRun = true
	res, err := dry.Execute(ctx)
	if err != nil {
		t.Fatalf("dry: %v", err)
	}
	if res.Identities != 1 || res.Repairs == 0 {
		t.Fatalf("dry run: identities=%d repairs=%d, want the lost identity found", res.Identities, res.Repairs)
	}
	if id, _ := steps.Identity(hash); id.Kind != "" {
		t.Fatal("the dry run wrote the identity: a check must not repair")
	}

	// The real pass restores it from the index.
	res, err = reconcile.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Identities != 1 {
		t.Fatalf("identities = %d, want 1", res.Identities)
	}
	id, err := steps.Identity(hash)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if id.Kind != entities.SourceKindURL || id.URL != link || id.Origin != entities.WikiLinkOrigin || id.WikiPage != "page-1" {
		t.Fatalf("restored identity = %+v, want the URL snapshot the index knew", id)
	}
}

// TestReconcileReportsMissingSummaries: a source whose derivation is COMPLETE but whose summary
// sidecar is gone (or was never written, which is every source from before the summary existed) is
// reported, not repaired in the pass — the summary is a model call and the app queues it on its own
// worker. A pending derivation is not reported: it has no claims worth an overview yet.
func TestReconcileReportsMissingSummaries(t *testing.T) {
	ctx := context.Background()
	sourcesDir := t.TempDir()
	store := objects.New(sourcesDir)
	idx := newFakeIndex()
	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	idx.m[src.Hash] = entities.Source{
		ID: src.ID, Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md", Title: "Caching",
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}).
			With(entities.StepDistill, entities.StepStatus{State: entities.StepOK}),
	}
	r := usecases.ReconcileVault{Index: idx, Store: store, Clock: fixedClock{}}

	res, err := r.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.MissingSummaries) != 1 || res.MissingSummaries[0] != src.Hash {
		t.Fatalf("missing summaries = %v, want the complete source", res.MissingSummaries)
	}
	if len(res.Invalidated) != 0 || res.Repairs != 0 {
		t.Fatalf("the pass repaired something for a missing summary: %+v", res)
	}

	// Once the sidecar exists, the source stops being reported.
	if pErr := store.PutRendered(src.Hash, entities.SidecarSummary, []byte("overview\n")); pErr != nil {
		t.Fatalf("summary: %v", pErr)
	}
	res, err = r.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.MissingSummaries) != 0 {
		t.Fatalf("missing summaries = %v after the sidecar was written, want none", res.MissingSummaries)
	}

	// A derivation that is still open owes nothing yet.
	idx.m[src.Hash] = entities.Source{
		ID: src.ID, Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md", Title: "Caching",
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}).
			With(entities.StepDistill, entities.StepStatus{State: entities.StepPending}),
	}
	if pErr := store.RemoveRendered(src.Hash, entities.SidecarSummary); pErr != nil && !os.IsNotExist(pErr) {
		t.Fatalf("remove summary: %v", pErr)
	}
	res, err = r.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.MissingSummaries) != 0 {
		t.Fatalf("missing summaries = %v for a pending derivation, want none", res.MissingSummaries)
	}
}

// TestReconcileReportsSourcesNeverAutoTagged: a completely distilled source that was never tagged is
// owed one derivation pass. The pass only reports it — deriving labels is a model call and the app
// queues it on the tag worker; a tagged source (even with an empty answer) is not reported again.
func TestReconcileReportsSourcesNeverAutoTagged(t *testing.T) {
	ctx := context.Background()
	sourcesDir := t.TempDir()
	store := objects.New(sourcesDir)
	idx := newFakeIndex()
	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	idx.m[src.Hash] = entities.Source{
		ID: src.ID, Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md", Title: "Caching",
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}).
			With(entities.StepDistill, entities.StepStatus{State: entities.StepOK}),
	}
	r := usecases.ReconcileVault{Index: idx, Store: store, Clock: fixedClock{}}
	res, err := r.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.MissingSourceTags) != 1 || res.MissingSourceTags[0] != src.Hash {
		t.Fatalf("missing source tags = %v, want the never-tagged source", res.MissingSourceTags)
	}

	idx.m[src.Hash] = entities.Source{
		ID: src.ID, Hash: src.Hash, Kind: entities.SourceKindMD, Ext: ".md", Title: "Caching",
		AutoTags: []string{"caching"}, AutoTagsAt: fixedClock{}.Now(),
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}).
			With(entities.StepDistill, entities.StepStatus{State: entities.StepOK}),
	}
	res, err = r.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.MissingSourceTags) != 0 {
		t.Fatalf("missing source tags = %v after tagging, want none", res.MissingSourceTags)
	}
}
