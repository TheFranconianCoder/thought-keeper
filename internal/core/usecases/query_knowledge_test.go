package usecases_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

type fakeSearch struct{ hits []interfaces.SearchHit }

func (f fakeSearch) Search(context.Context, string, string, int) ([]interfaces.SearchHit, error) {
	return f.hits, nil
}

func (f fakeSearch) SearchExact(context.Context, string, string, int) ([]interfaces.SearchHit, error) {
	return f.hits, nil
}

type fakeAskStore struct{ saved []entities.Ask }

func (f *fakeAskStore) Write(_ context.Context, a entities.Ask) error {
	f.saved = append(f.saved, a)
	return nil
}

func (f *fakeAskStore) Get(context.Context, ulid.ULID) (entities.Ask, error) {
	return entities.Ask{}, nil
}

func (f *fakeAskStore) List(context.Context, int, int) ([]entities.Ask, error) { return f.saved, nil }
func (f *fakeAskStore) Delete(context.Context, ulid.ULID) error                { return nil }
func (f *fakeAskStore) Normalize(context.Context) (int, error)                 { return 0, nil }

func TestQueryAnswersWithCitations(t *testing.T) {
	ctx := context.Background()
	runRepo := &fakeRunRepo{}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		{
			Message: interfaces.AgentMessage{Role: "assistant", ToolCalls: []interfaces.AgentToolCall{
				{ID: "c1", Name: "search", Arguments: `{"query":"caching"}`},
			}},
			TotalTokens: 50,
		},
		{
			Message:     interfaces.AgentMessage{Role: "assistant", Content: "Caching is subtle. See [[claim:01J8ZC5V9P0R7T2QK4W6Y8B3ND|Caching is subtle]].\n\nSources: [[claim:01J8ZC5V9P0R7T2QK4W6Y8B3ND|Caching is subtle]]"},
			TotalTokens: 20,
		},
	}}
	asks := &fakeAskStore{}
	uc := usecases.Query{
		Atoms: &fakeAtomRepo{},
		Lexical: fakeSearch{hits: []interfaces.SearchHit{
			{ID: ulid.Make().String(), Kind: "atom", Title: "Caching is subtle", Snippet: "s"},
		}},
		Asks: asks, Runs: runRepo, Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}

	res, err := uc.Ask(ctx, ulid.Make(), "What about caching?")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !strings.Contains(res.Answer, "](#/claims/") || strings.Contains(res.Answer, "[[claim:") {
		t.Errorf("answer lacks a normalised citation: %q", res.Answer)
	}
	if runRepo.run.Status != entities.RunSucceeded || runRepo.run.Op != entities.RunQuery {
		t.Errorf("run = %+v", runRepo.run)
	}
	if len(runRepo.steps) != 1 || runRepo.steps[0].Tool != "search" {
		t.Errorf("steps = %+v", runRepo.steps)
	}
	if runRepo.run.TotalTokens != 70 {
		t.Errorf("tokens = %d, want 70", runRepo.run.TotalTokens)
	}
	if len(asks.saved) != 1 || asks.saved[0].Question != "What about caching?" {
		t.Errorf("saved asks = %+v, want the asked question", asks.saved)
	}
}

// TestScopedQueryStaysInOneSource: a question asked from a source may only use that source. The
// search is issued with the scope, the vault-wide tools are not offered, and the saved ask carries
// the source so "ask again" can stay scoped.
func TestScopedQueryStaysInOneSource(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	runRepo := &fakeRunRepo{}
	search := &fakeScopedSearch{hits: []interfaces.SearchHit{
		{ID: ulid.Make().String(), Kind: "atom", Title: "A claim of the source", Snippet: "s"},
	}}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		{
			Message: interfaces.AgentMessage{Role: "assistant", ToolCalls: []interfaces.AgentToolCall{
				{ID: "c1", Name: "search", Arguments: `{"query":"claim"}`},
			}},
			TotalTokens: 40,
		},
		{
			Message:     interfaces.AgentMessage{Role: "assistant", Content: "The source says so."},
			TotalTokens: 10,
		},
	}}
	asks := &fakeAskStore{}
	uc := usecases.Query{
		Atoms: &fakeAtomRepo{}, Lexical: search, Asks: asks, Runs: runRepo,
		Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}

	if _, err := uc.AskScoped(ctx, ulid.Make(), "What does this source say?", hash); err != nil {
		t.Fatalf("scoped query: %v", err)
	}
	if !search.scoped || search.scope != hash {
		t.Errorf("search scope = %q (called %v), want %q", search.scope, search.scoped, hash)
	}
	if len(asks.saved) != 1 || asks.saved[0].Source != hash {
		t.Errorf("saved asks = %+v, want the source on the ask", asks.saved)
	}
	if len(gw.requests) == 0 || !strings.Contains(gw.requests[0].Messages[0].Content, "ONE source") {
		t.Error("the scoped system prompt was not used")
	}
	for _, tool := range gw.requests[0].Tools {
		if tool.Name == "list_sources" || tool.Name == "list_topics" {
			t.Errorf("scoped query was offered the vault-wide tool %q", tool.Name)
		}
	}
}

// TestQuerySpentBudgetIsRecorded: a run that spent its turns still answers, and the run says so.
// The flag is what keeps a partial answer from looking complete in the API.
func TestQuerySpentBudgetIsRecorded(t *testing.T) {
	runRepo := &fakeRunRepo{}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		{
			Message: interfaces.AgentMessage{Role: "assistant", ToolCalls: []interfaces.AgentToolCall{
				{ID: "c1", Name: "search", Arguments: `{"query":"caching"}`},
			}},
			TotalTokens: 40,
		},
		{Message: interfaces.AgentMessage{Role: "assistant", Content: "The claims I read say this."}, TotalTokens: 10},
	}}
	uc := usecases.Query{
		Atoms: &fakeAtomRepo{}, Lexical: fakeSearch{}, Asks: &fakeAskStore{}, Runs: runRepo,
		Gateway: gw, Model: "test", MaxIterations: 1, Clock: fixedClock{},
	}
	res, err := uc.Ask(context.Background(), ulid.Make(), "What about caching?")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !res.Truncated || !runRepo.run.Truncated {
		t.Errorf("truncated = %t (run %t), want both true after a spent budget", res.Truncated, runRepo.run.Truncated)
	}
	if res.Answer == "" || runRepo.run.Status != entities.RunSucceeded {
		t.Errorf("answer = %q, status = %q; want the partial answer on a succeeded run", res.Answer, runRepo.run.Status)
	}
}

// TestSearchAnswersOnePage: a search answer is one page with next_offset, so the model reads
// evidence instead of the beginning of a truncated JSON array. The offset names where the page
// starts; a hit beyond the page is what says there is a next one.
func TestSearchAnswersOnePage(t *testing.T) {
	hits := make([]interfaces.SearchHit, 5)
	for i := range hits {
		hits[i] = interfaces.SearchHit{ID: ulid.Make().String(), Kind: "atom", Title: "a claim", Snippet: "s"}
	}
	runRepo := &fakeRunRepo{}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		{
			Message: interfaces.AgentMessage{Role: "assistant", ToolCalls: []interfaces.AgentToolCall{
				{ID: "c1", Name: "search", Arguments: `{"query":"caching","limit":2,"offset":2}`},
			}},
			TotalTokens: 40,
		},
		{Message: interfaces.AgentMessage{Role: "assistant", Content: "Answer."}, TotalTokens: 10},
	}}
	uc := usecases.Query{
		Atoms: &fakeAtomRepo{}, Lexical: fakeSearch{hits: hits}, Asks: &fakeAskStore{},
		Runs: runRepo, Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}
	if _, err := uc.Ask(context.Background(), ulid.Make(), "What about caching?"); err != nil {
		t.Fatalf("query: %v", err)
	}
	var toolContent string
	for _, m := range gw.requests[1].Messages {
		if m.Role == "tool" {
			toolContent = m.Content
		}
	}
	var page struct {
		Offset     int              `json:"offset"`
		NextOffset int              `json:"next_offset"`
		Hits       []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(toolContent), &page); err != nil {
		t.Fatalf("search answer is not JSON: %v\n%s", err, toolContent)
	}
	if page.Offset != 2 || page.NextOffset != 4 || len(page.Hits) != 2 {
		t.Errorf("page = offset %d, next %d, hits %d; want 2, 4, 2", page.Offset, page.NextOffset, len(page.Hits))
	}
}

type fakeScopedSearch struct {
	fakeSearch
	scoped bool
	scope  string
}

func (f *fakeScopedSearch) SearchInSource(_ context.Context, _ string, source string, _ int) ([]interfaces.SearchHit, error) {
	f.scoped = true
	f.scope = source
	return f.hits, nil
}
