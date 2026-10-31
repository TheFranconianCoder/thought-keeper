package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// Searcher runs a lexical search over claims and source metadata. A handler holds this value; nil means
// the feature is not wired.
type Searcher func(ctx context.Context, query, layer string, limit int) ([]partialentities.PartialHit, error)

// Search returns matches. The default layer is CLAIMS: search is claim-first.
func (uc Query) Search(ctx context.Context, query, layer string, limit int) ([]partialentities.PartialHit, error) {
	if layer == "" {
		layer = "atoms"
	}
	hits, err := uc.Lexical.Search(ctx, query, layer, limit)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, partialentities.PartialHit{ID: h.ID, Kind: h.Kind, Title: h.Title, Snippet: h.Snippet})
	}
	return out, nil
}
