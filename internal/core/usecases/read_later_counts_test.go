package usecases_test

import (
	"context"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// fakeShelfCounter answers the queue's numbers without a vault.
type fakeShelfCounter struct {
	open, from int
	byInterest map[string]int
}

func (f fakeShelfCounter) CountReadLater(context.Context) (int, error) { return f.open, nil }
func (f fakeShelfCounter) CountReadLaterFromInterests(context.Context) (int, error) {
	return f.from, nil
}

func (f fakeShelfCounter) CountReadLaterByInterest(context.Context) (map[string]int, error) {
	return f.byInterest, nil
}
func (f fakeShelfCounter) CountReadLaterVia(context.Context, string) (int, error) { return 0, nil }
func (f fakeShelfCounter) CountFavorites(context.Context) (int, error)            { return 0, nil }

// TestSourcesReadLaterCountsNameTheRules: the queue's split carries the rules themselves, biggest
// share first, and a tie is broken by the slug — the same queue must read the same way twice.
func TestSourcesReadLaterCountsNameTheRules(t *testing.T) {
	uc := usecases.Sources{Shelf: fakeShelfCounter{
		open:       12,
		byInterest: map[string]int{"storage": 3, "caching": 5, "podcast": 3},
	}}
	got, err := uc.ReadLaterCounts(context.Background())
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if got.Open != 12 {
		t.Errorf("queue = %d, want 12 open", got.Open)
	}
	want := []usecases.QueueInterest{{Slug: "caching", Count: 5}, {Slug: "podcast", Count: 3}, {Slug: "storage", Count: 3}}
	if len(got.Interests) != len(want) {
		t.Fatalf("rules = %+v, want %+v", got.Interests, want)
	}
	for i := range want {
		if got.Interests[i] != want[i] {
			t.Fatalf("rules = %+v, want %+v", got.Interests, want)
		}
	}
}
