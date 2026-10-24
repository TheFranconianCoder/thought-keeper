package usecases_test

import (
	"context"
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
	uc := usecases.QueryKnowledge{
		Atoms: &fakeAtomRepo{},
		Search: fakeSearch{hits: []interfaces.SearchHit{
			{ID: ulid.Make().String(), Kind: "atom", Title: "Caching is subtle", Snippet: "s"},
		}},
		Asks: asks, Runs: runRepo, Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}

	res, err := uc.Execute(ctx, ulid.Make(), "What about caching?")
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
