package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// RebuildTreesResult reports what a rebuild did.
type RebuildTreesResult struct {
	Sources int
	Built   int
	Failed  int
}

// RebuildTreesService rebuilds the outline of every source that needs one. A handler holds this value;
// nil means the feature is not wired.
type RebuildTreesService func(ctx context.Context) (RebuildTreesResult, error)

// Rebuild rebuilds every outline that needs one.
func (uc Trees) Rebuild(ctx context.Context) (RebuildTreesResult, error) {
	srcs, err := uc.Index.ListSources(ctx, entities.SourceFilter{}, 1_000_000, 0)
	if err != nil {
		return RebuildTreesResult{}, err
	}
	var res RebuildTreesResult
	for _, s := range srcs {
		// Nothing to outline without a readable capture: a bookmark, a blocked URL (its snapshot is a
		// note about the gate, not the page) and a failed extraction have no parts to build from. A
		// feed or a sitemap is a LIST of sources, not a document to outline.
		if s.Bookmark || s.OwnsChildren() || s.Steps.State(entities.StepCapture) == entities.StepFailed {
			continue
		}
		res.Sources++
		if _, err := uc.Build(ctx, s.Hash); err != nil {
			res.Failed++
			continue
		}
		res.Built++
	}
	return res, nil
}
