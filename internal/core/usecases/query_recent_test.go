package usecases_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// recentQueryStore serves the overview sidecar a recency answer reads. The other SourceStore
// methods are embedded and unused by the query.
type recentQueryStore struct {
	interfaces.SourceStore
	summaries map[string]string
}

func (s *recentQueryStore) OpenRendered(hash, suffix string) (io.ReadCloser, error) {
	text, ok := s.summaries[hash+suffix]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(text)), nil
}

func addRecentSource(t *testing.T, idx *fakeIndex, hash, title string, at time.Time) {
	t.Helper()
	if err := idx.UpsertSource(context.Background(), entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md", Path: hash + ".md",
		Title: title, ImportedAt: at,
		Steps: entities.StepSet{entities.StepDistill: {State: entities.StepOK}},
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
}

// toolMessage is the answer the model saw after its last tool call: the tool role is what carries
// the data, and it is the only place a test can prove the tool returned the right thing.
func toolMessage(req interfaces.AgentRequest) string {
	last := ""
	for _, m := range req.Messages {
		if m.Role == "tool" {
			last = m.Content
		}
	}
	return last
}

func TestQueryRecentSourcesUsesTheOverview(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("a1", 32)
	idx := newFakeIndex()
	addRecentSource(t, idx, hash, "A recent article", fixedClock{}.Now().Add(-time.Hour))
	store := &recentQueryStore{summaries: map[string]string{
		hash + entities.SidecarSummary: "The article argues that caches need a versioned key.",
	}}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_sources", `{"since":"24h"}`),
		finalResponse("In den letzten 24 Stunden ist eine Quelle hinzugekommen. [[source:" + hash + "|A recent article]]"),
	}}
	uc := usecases.QueryKnowledge{
		Atoms: &fakeAtomRepo{}, Index: idx, Store: store,
		Runs: &fakeRunRepo{}, Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}

	res, err := uc.Execute(ctx, ulid.Make(), "Fasse die jüngsten Sources der letzten 24h zusammen.")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !strings.Contains(res.Answer, "#/sources/"+hash) {
		t.Fatalf("answer = %q, want a source link", res.Answer)
	}
	if strings.Contains(res.Answer, "[[source:") {
		t.Fatalf("raw source citation survived: %q", res.Answer)
	}
	tool := toolMessage(gw.requests[1])
	if !strings.Contains(tool, "A recent article") || !strings.Contains(tool, "versioned key") {
		t.Fatalf("list_sources result = %q, want the title and the overview excerpt", tool)
	}
}

func TestQueryRecentSourcesEmptyWindow(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("b2", 32)
	idx := newFakeIndex()
	addRecentSource(t, idx, hash, "An old article", fixedClock{}.Now().Add(-48*time.Hour))
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_sources", `{"since":"24h"}`),
		finalResponse("In den letzten 24 Stunden ist keine Quelle hinzugekommen."),
	}}
	uc := usecases.QueryKnowledge{
		Atoms: &fakeAtomRepo{}, Index: idx,
		Runs: &fakeRunRepo{}, Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}

	res, err := uc.Execute(ctx, ulid.Make(), "Fasse die jüngsten Sources der letzten 24h zusammen.")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	tool := toolMessage(gw.requests[1])
	if !strings.Contains(tool, `"total":0`) {
		t.Fatalf("list_sources result = %q, want an empty window", tool)
	}
	if strings.Contains(tool, "An old article") {
		t.Fatalf("list_sources leaked a source outside the window: %q", tool)
	}
	if strings.Contains(res.Answer, "#/sources/") {
		t.Fatalf("an empty window was answered with a source: %q", res.Answer)
	}
}

func TestQueryRecentSourcesFallsBackToClaims(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("c3", 32)
	atomID := ulid.Make()
	idx := newFakeIndex()
	addRecentSource(t, idx, hash, "A source without an overview", fixedClock{}.Now().Add(-time.Hour))
	atoms := &fakeAtomRepo{saved: []entities.Atom{{
		ID: atomID, Topic: "caching", Claim: "A cache needs a versioned key.",
	}}}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_sources", `{"since":"24h"}`),
		toolResponse("read_source_summary", `{"hash":"`+hash+`"}`),
		finalResponse("Die Quelle sagt, dass ein Cache einen versionierten Schlüssel braucht ([[claim:" + atomID.String() + "|A cache needs a versioned key.]])."),
	}}
	uc := usecases.QueryKnowledge{
		Atoms: atoms, Index: idx,
		Runs: &fakeRunRepo{}, Gateway: gw, Model: "test", MaxIterations: 5, Clock: fixedClock{},
	}

	res, err := uc.Execute(ctx, ulid.Make(), "Fasse die jüngsten Sources der letzten 24h zusammen.")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	tool := toolMessage(gw.requests[2])
	if !strings.Contains(tool, `"available":false`) || !strings.Contains(tool, "A cache needs a versioned key.") {
		t.Fatalf("read_source_summary result = %q, want the claim fallback", tool)
	}
	if !strings.Contains(res.Answer, "#/claims/"+atomID.String()) {
		t.Fatalf("answer = %q, want the fallback claim cited", res.Answer)
	}
}

// TestQueryRecentSourcesPagesTheWholeWindow: twelve sources must not be silently reduced to the
// first eight. The tool reports next_offset, the model asks for the second page, and the answer can
// cover every source the window holds.
func TestQueryRecentSourcesPagesTheWholeWindow(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	now := fixedClock{}.Now()
	for i := 1; i <= 12; i++ {
		hash := fmt.Sprintf("%064x", i)
		addRecentSource(t, idx, hash, fmt.Sprintf("Source %02d", i), now.Add(-time.Duration(i)*time.Minute))
	}

	var answer strings.Builder
	answer.WriteString("Die 12 Quellen im Fenster:\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&answer, "- Source %02d\n", i)
	}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_sources", `{"since":"24h"}`),
		toolResponse("list_sources", `{"since":"24h","offset":8}`),
		finalResponse(answer.String()),
	}}
	uc := usecases.QueryKnowledge{
		Atoms: &fakeAtomRepo{}, Index: idx,
		Runs: &fakeRunRepo{}, Gateway: gw, Model: "test", MaxIterations: 6, Clock: fixedClock{},
	}

	res, err := uc.Execute(ctx, ulid.Make(), "Fasse die jüngsten Sources der letzten 24h zusammen.")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	first := toolMessage(gw.requests[1])
	if !strings.Contains(first, `"next_offset":8`) || !strings.Contains(first, `"total":12`) {
		t.Fatalf("first page = %q, want 8 of 12 and a next offset", first)
	}
	second := toolMessage(gw.requests[2])
	if !strings.Contains(second, `"next_offset":0`) || !strings.Contains(second, `"total":12`) {
		t.Fatalf("second page = %q, want the last 4 and no next offset", second)
	}
	for i := 9; i <= 12; i++ {
		if !strings.Contains(second, fmt.Sprintf("Source %02d", i)) {
			t.Fatalf("second page = %q, want Source %02d", second, i)
		}
	}
	if got := strings.Count(res.Answer, "Source "); got != 12 {
		t.Fatalf("answer covers %d sources, want all 12: %q", got, res.Answer)
	}
}

func TestQueryListAtomsBySource(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("d4", 32)
	atomID := ulid.Make()
	idx := newFakeIndex()
	addRecentSource(t, idx, hash, "A source", fixedClock{}.Now())
	atoms := &fakeAtomRepo{saved: []entities.Atom{{
		ID: atomID, Topic: "caching", Claim: "A cache needs a versioned key.",
	}}}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_atoms", `{"source":"`+hash+`"}`),
		finalResponse("The source says that a cache needs a versioned key."),
	}}
	uc := usecases.QueryKnowledge{
		Atoms: atoms, Index: idx,
		Runs: &fakeRunRepo{}, Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}

	if _, err := uc.Execute(ctx, ulid.Make(), "What does the newest source say?"); err != nil {
		t.Fatalf("query: %v", err)
	}
	tool := toolMessage(gw.requests[1])
	if !strings.Contains(tool, atomID.String()) || !strings.Contains(tool, "A cache needs a versioned key.") {
		t.Fatalf("list_atoms result = %q, want the source's claim", tool)
	}
}
