package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// Search runs a lexical search over claims and source outlines. There is no page layer to search.
type Search struct {
	Search interfaces.SearchRepository
}

// Execute returns matches. The default layer is CLAIMS: search is claim-first.
func (uc Search) Execute(ctx context.Context, query, layer string, limit int) ([]partialentities.PartialHit, error) {
	if layer == "" {
		layer = "atoms"
	}
	hits, err := uc.Search.Search(ctx, query, layer, limit)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, partialentities.PartialHit{ID: h.ID, Kind: h.Kind, Title: h.Title, Snippet: h.Snippet})
	}
	return out, nil
}
