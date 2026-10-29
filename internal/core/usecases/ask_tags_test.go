package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// TestAskTagsUseTheCitedSources: a digest that cites only source overviews has no claim topic to
// borrow, so its tags come from the sources' own shelves. A claim answer still wins: a claim's
// topic is sharper than the shelf it came from.
func TestAskTagsUseTheCitedSources(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("e5", 32)
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		Hash: hash, Tags: []string{"caching"}, AutoTags: []string{"distributed-systems"},
	}
	answer := "Eine Quelle. ([1](#cite-1))\n\nSources:\n1. [Titel](#/sources/" + hash + ")"
	tags := usecases.AskTags(ctx, "Fasse die letzten 24 Stunden zusammen.", answer, nil, idx)
	if len(tags) != 2 || tags[0] != "caching" || tags[1] != "distributed-systems" {
		t.Fatalf("tags = %v, want the source's two shelves", tags)
	}

	atomID := ulid.Make()
	atoms := &fakeAtomRepo{saved: []entities.Atom{{ID: atomID, Topic: "go"}}}
	answer = "Ein Claim. ([1](#cite-1))\n\nSources:\n1. [Ein Claim](#/claims/" + atomID.String() + ")"
	tags = usecases.AskTags(ctx, "Was ist mit Go?", answer, atoms, idx)
	if len(tags) != 1 || tags[0] != "go" {
		t.Fatalf("tags = %v, want the claim's topic to win", tags)
	}
}
