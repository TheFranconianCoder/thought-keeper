package sqlc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	atomstore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	treeStore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/tree"
)

// partAgent answers from the PROMPT: parts run in parallel, so a scripted sequence would be a coin
// flip. It writes one claim per part in the write pass and answers COMPLETE in the audit.
type partAgent struct {
	mu      sync.Mutex
	written []string
	audits  int
}

func (a *partAgent) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	last := req.Messages[len(req.Messages)-1].Content
	tools := 0
	for _, m := range req.Messages {
		if m.Role == "tool" {
			tools++
		}
	}
	if strings.Contains(last, "Check ONE part") {
		a.mu.Lock()
		a.audits++
		a.mu.Unlock()
		return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: `{"complete":true,"claims":[]}`}}, nil
	}
	id := nodeIDIn(last)
	if id == "" || tools > 0 {
		return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: `{"claims":[]}`}}, nil
	}
	args, _ := json.Marshal(map[string]any{
		"branch": "eng", "claim": "A claim about part " + id, "body": "b", "nodes": []string{id},
	})
	a.mu.Lock()
	a.written = append(a.written, id)
	a.mu.Unlock()
	// Tools only read: the claims are the ANSWER, and the app records them.
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{
		Role:    "assistant",
		Content: `{"claims":[` + string(args) + `]}`,
	}}, nil
}

func nodeIDIn(prompt string) string {
	const marker = "node "
	_, after, ok := strings.Cut(prompt, marker)
	if !ok {
		return ""
	}
	rest := after
	if j := strings.IndexByte(rest, ' '); j > 0 {
		return rest[:j]
	}
	return ""
}

// TestDistillCoversEveryTextNode is the regression guard for the run that "finished" after 5 atoms
// out of 25 nodes, and for the one that produced 36 where the same document had produced 44: the
// work unit is ONE text-bearing node, so no node can be left out because another node was covered,
// and every part is read against its claims once before it is recorded as done.
func TestDistillCoversEveryTextNode(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(t.TempDir())
	trees := treeStore.New(t.TempDir())
	index := NewSourceRepository(db)
	atomsRepo := NewAtomRepository(db)
	parts := NewPartRepository(db)
	atomFiles := atomstore.New(t.TempDir())

	body := "# Doc\n\ntext one\n\ntext two\n"
	src, err := usecases.IntakeSource{Store: store, Index: index, Clock: testClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "doc.md", Reader: strings.NewReader(body)})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	// One part with two nodes that carry text — the shape that used to be ONE unit. The outline
	// stores the REFERENCE (the range in the readable text) plus the hash, never the text itself.
	one, two := strings.Index(body, "text one"), strings.Index(body, "text two")
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		{NodeID: "0.1", Depth: 1, Title: "First part", TextStart: one, TextEnd: one + len("text one"), TextHash: "h1"},
		{NodeID: "0.1.1", Depth: 2, Title: "Second part", TextStart: two, TextEnd: two + len("text two"), TextHash: "h2"},
	}
	if err := trees.Write(ctx, src.Hash, nodes); err != nil {
		t.Fatalf("tree sidecar: %v", err)
	}

	agent := &partAgent{}
	az := usecases.DistillSource{
		Index: index, Trees: trees, Store: store, Atoms: atomFiles, AtomRepo: atomsRepo,
		PartRepo: parts, PartsPerRun: 4, Parallel: 2,
		Gateway: agent, Model: "test", MaxIterations: 4, Clock: testClock{},
	}
	if _, err := az.Distill(ctx, src.Hash, usecases.NewStepRecorder(ctx, NewRunRepository(db), ulid.Make(), nil)); err != nil {
		t.Fatalf("distill: %v", err)
	}

	atoms, err := atomsRepo.ListAtomsBySource(ctx, src.Hash)
	if err != nil {
		t.Fatalf("atoms: %v", err)
	}
	if len(atoms) != 2 {
		t.Errorf("atoms = %d, want 2 (one per text node)", len(atoms))
	}
	// Provenance is what says a node's text became a claim (ListAtomsBySource does not hydrate it).
	refs, err := atomsRepo.NodeRefsBySource(ctx, src.Hash)
	if err != nil {
		t.Fatalf("node refs: %v", err)
	}
	saw := map[string]int{}
	for _, ids := range refs {
		for _, id := range ids {
			saw[id]++
		}
	}
	for _, id := range []string{"0.1", "0.1.1"} {
		if saw[id] == 0 {
			t.Errorf("node %s has no claim at all; provenance=%v", id, saw)
		}
	}
	// Every part was read against its own claims before being recorded.
	if agent.audits != 2 {
		t.Errorf("audits = %d, want one per part", agent.audits)
	}
	states, err := parts.PartStates(ctx, src.ID)
	if err != nil {
		t.Fatalf("states: %v", err)
	}
	for _, id := range []string{"0.1", "0.1.1"} {
		if st, ok := states[id]; !ok || st.State != entities.PartDone {
			t.Errorf("part %s state = %+v, want done", id, st)
		}
	}
}

// slug is the tiny slug the atom store would build; the test only needs something unique per claim.
func slug(s string) string {
	out := strings.ToLower(s)
	out = strings.ReplaceAll(out, " ", "-")
	return out
}

// TestPendingCompileSweepFindsWhatAFailedRunLeft: a run that dies before or in the compile leaves
// claims no page cites. That backlog is the resume point — it must be discoverable, so the start-up
// sweep (and the retry after a transient failure) can pick it up instead of leaving the source
// half-compiled forever.
func TestPendingCompileSweepFindsWhatAFailedRunLeft(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := NewSourceRepository(db)
	atomsRepo := NewAtomRepository(db)
	clusters := NewClusterRepository(db)
	sweep := usecases.SourcesWithUnclustered{Index: index}

	mk := func(name string) string {
		src, err := usecases.IntakeSource{Store: objects.New(t.TempDir()), Index: index, Clock: testClock{}}.
			Execute(ctx, usecases.IntakeInput{Name: name, Reader: strings.NewReader("# " + name + "\n\nbody\n")})
		if err != nil {
			t.Fatalf("intake %s: %v", name, err)
		}
		// A built tree is the precondition: a source without one is not "pending compile".
		if err := index.SetSourceTree(ctx, src.Hash, string(entities.TreeBuilt), name, 0); err != nil {
			t.Fatalf("tree state: %v", err)
		}
		return src.Hash
	}
	uncompiled := mk("uncompiled.md")
	compiled := mk("compiled.md")
	empty := mk("empty.md") // built tree, no atoms at all: the other sweep's job

	// One claim per source; only the associated one ends up in a cluster.
	claim := func(hash, text string) entities.Atom {
		a := entities.Atom{
			ID: ulid.Make(), Topic: "eng", Path: "eng/" + slug(text) + ".md",
			Claim: text, Body: "b", Status: entities.AtomActive,
			Sources: []entities.AtomSource{{Hash: hash}},
		}
		if err := atomsRepo.SaveAtom(ctx, a); err != nil {
			t.Fatalf("save atom: %v", err)
		}
		return a
	}
	pendingAtom := claim(uncompiled, "uncited claim")
	onPage := claim(compiled, "cited claim")

	if err := clusters.SaveCluster(ctx, entities.Cluster{
		ID: ulid.Make(), Atoms: []ulid.ULID{onPage.ID},
		Created: testClock{}.Now(), Updated: testClock{}.Now(),
	}); err != nil {
		t.Fatalf("save cluster: %v", err)
	}

	got, err := sweep.Execute(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	hashes := map[string]bool{}
	for _, s := range got {
		hashes[s.Hash] = true
	}
	if !hashes[uncompiled] {
		t.Error("a source whose claims are in no cluster was not found — a failed association stays broken")
	}
	if hashes[compiled] {
		t.Error("a fully associated source was queued again")
	}
	if hashes[empty] {
		t.Error("a source without any atom is the missing-atoms sweep's job, not this one")
	}
	_ = pendingAtom
}

// TestOpenPartsSweepFindsAHalfDerivedBook: a 500-page book is derived over many runs, and a restart
// must CONTINUE it. "Has no atoms" is only true before the first run — a book that has moved through
// some of its parts has atoms and would otherwise never be swept again.
func TestOpenPartsSweepFindsAHalfDerivedBook(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := NewSourceRepository(db)
	parts := NewPartRepository(db)
	atomsRepo := NewAtomRepository(db)
	sweep := usecases.SourcesWithOpenParts{Index: index}

	src, err := usecases.IntakeSource{Store: objects.New(t.TempDir()), Index: index, Clock: testClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "book.md", Reader: strings.NewReader("# Book\\n\\nbody\\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Book"},
		{NodeID: "0.1", Depth: 1, Title: "Part one", Text: "text one"},
		{NodeID: "0.2", Depth: 1, Title: "Part two", Text: "text two"},
	}
	if err := index.ReplaceSourceNodes(ctx, src.ID, nodes); err != nil {
		t.Fatalf("nodes: %v", err)
	}
	if err := index.SetSourceTree(ctx, src.Hash, string(entities.TreeBuilt), "Book", 0); err != nil {
		t.Fatalf("tree state: %v", err)
	}
	// One part derived, one open: the book is half done.
	now := testClock{}.Now()
	saveClaim := func(node string) {
		err := atomsRepo.SaveAtom(ctx, entities.Atom{
			ID: ulid.Make(), Topic: "test", Claim: "claim for " + node, Body: "body",
			Type: entities.AtomClaim, Status: entities.AtomActive, CreatedAt: now, UpdatedAt: now,
			Path:    "atoms/" + node + ".md",
			Sources: []entities.AtomSource{{Hash: src.Hash, NodeID: node}},
		})
		if err != nil {
			t.Fatalf("save atom %s: %v", node, err)
		}
	}
	saveClaim("0.1")
	if err := parts.SetPartState(ctx, src.ID, entities.PartState{
		NodeID: "0.1", State: entities.PartDone, Atoms: 1, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("part state: %v", err)
	}
	got, err := sweep.Execute(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(got) != 1 || got[0].Hash != src.Hash {
		t.Fatalf("sweep = %+v, want the half-derived book", got)
	}

	// Once every part is done AND has its claim, the book is finished and the sweep leaves it alone.
	saveClaim("0.2")
	if err := parts.SetPartState(ctx, src.ID, entities.PartState{
		NodeID: "0.2", State: entities.PartDone, Atoms: 1, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("part state: %v", err)
	}
	gone, err := sweep.Execute(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(gone) != 0 {
		t.Errorf("sweep = %+v, want nothing once the derivation is complete", gone)
	}
	_ = atomsRepo
}
