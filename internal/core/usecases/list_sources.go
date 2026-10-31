package usecases

import (
	"context"
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ListSources lists indexed sources, optionally filtered by kind.
type ListSources struct {
	Index interfaces.IndexRepository
}

// Execute returns sources ordered by import time (newest first), narrowed by filter.
func (uc ListSources) Execute(ctx context.Context, filter entities.SourceFilter, limit, offset int) ([]partialentities.PartialSource, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	srcs, err := uc.Index.ListSources(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return out, nil
}

// Count is how many sources the FILTERED list has in total: a page needs it to know whether it is
// the whole list, and a windowed scroller needs it for its height.
func (uc ListSources) Count(ctx context.Context, filter entities.SourceFilter) (int, error) {
	return uc.Index.CountSources(ctx, filter)
}

// ListSourceFacets is the tag cloud's data: how often each human or derived source tag occurs, and
// how many sources have a human-attached snapshot. It is a facet over the vault, not over the window
// the list happens to have loaded.
type ListSourceFacets struct {
	Index interfaces.IndexRepository
	// Shelf answers the star and queue counts. Nil (a test, a bare app) leaves them zero.
	Shelf interfaces.ShelfCounter
}

// SourceFacetsResult is the facet payload.
type SourceFacetsResult struct {
	Tags     []partialentities.NameUses
	Attached int
	// The vault-wide counts behind the filter chips and the queue banner.
	Favorites          int
	ReadLater          int
	ReadLaterInterests int
}

// Execute counts the tags, the attached snapshots and the reader's marks.
func (uc ListSourceFacets) Execute(ctx context.Context) (SourceFacetsResult, error) {
	counts, err := uc.Index.SourceTagCounts(ctx)
	if err != nil {
		return SourceFacetsResult{}, err
	}
	out := make([]partialentities.NameUses, 0, len(counts))
	for name, uses := range counts {
		out = append(out, partialentities.NameUses{Name: name, Uses: uses})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Uses != out[j].Uses {
			return out[i].Uses > out[j].Uses
		}
		return out[i].Name < out[j].Name
	})
	attached, err := uc.Index.CountAttachedSources(ctx)
	if err != nil {
		return SourceFacetsResult{}, err
	}
	res := SourceFacetsResult{Tags: out, Attached: attached}
	if uc.Shelf != nil {
		if n, cErr := uc.Shelf.CountFavorites(ctx); cErr == nil {
			res.Favorites = n
		}
		if n, cErr := uc.Shelf.CountReadLater(ctx); cErr == nil {
			res.ReadLater = n
		}
		if n, cErr := uc.Shelf.CountReadLaterFromInterests(ctx); cErr == nil {
			res.ReadLaterInterests = n
		}
	}
	return res, nil
}

// GetSource returns one source by its content hash.
type GetSource struct {
	Index interfaces.IndexRepository
}

// Execute returns the source with the given SHA-256 hash.
func (uc GetSource) Execute(ctx context.Context, hash string) (partialentities.PartialSource, error) {
	if !entities.IsSHA256Hex(hash) {
		return partialentities.PartialSource{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	out := partialentities.PartialFromSource(src)
	// A wiki page's children are recorded under the PAGE id, not under the representation's hash.
	if n, cErr := uc.Index.CountSourceChildren(ctx, src.Hash, entities.WikiIDFromOriginalName(src.OriginalName)); cErr == nil {
		out.Children = n
	}
	return out, nil
}

// SourceChildren lists the sources one source OWNS: the pages of a sitemap, or the URLs a wiki page
// brought into the vault. It is the data behind the detail view's Sub-sources tab.
type SourceChildren struct {
	Index interfaces.IndexRepository
}

// SourceChildrenResult is a source's sub-sources as the wire sees them.
type SourceChildrenResult struct {
	Sources []partialentities.PartialSource
	Total   int
}

// Execute returns the sub-sources of the source with this hash.
func (uc SourceChildren) Execute(ctx context.Context, hash string) (SourceChildrenResult, error) {
	if !entities.IsSHA256Hex(hash) {
		return SourceChildrenResult{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return SourceChildrenResult{}, err
	}
	srcs, err := uc.Index.ListSourceChildren(ctx, src.Hash, entities.WikiIDFromOriginalName(src.OriginalName))
	if err != nil {
		return SourceChildrenResult{}, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return SourceChildrenResult{Sources: out, Total: len(out)}, nil
}

// SourceHasAtoms reports whether a source has been distilled already.
type SourceHasAtoms struct {
	Index interfaces.IndexRepository
}

// Execute returns true when at least one atom cites the source.
func (uc SourceHasAtoms) Execute(ctx context.Context, hash string) (bool, error) {
	if !entities.IsSHA256Hex(hash) {
		return false, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	return uc.Index.SourceHasAtoms(ctx, hash)
}

// SourcesWithoutTree lists sources whose tree was never built.
type SourcesWithoutTree struct {
	Index interfaces.IndexRepository
}

// Execute returns sources with tree_state "none".
func (uc SourcesWithoutTree) Execute(ctx context.Context) ([]partialentities.PartialSource, error) {
	srcs, err := uc.Index.SourcesWithNoTree(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return out, nil
}

// SourcesWithoutAtoms lists sources with a built tree but no atoms yet (the
// derived "pending distill" document status).
type SourcesWithoutAtoms struct {
	Index interfaces.IndexRepository
}

// Execute returns the sources with a built tree but no atoms.
// SourcesWithOpenParts lists sources whose derivation is not finished: a book that has moved
// through some of its parts still has parts without a claim. This is how a long document resumes
// after a restart — the sweep needs the state, not a flag.
type SourcesWithOpenParts struct {
	Index interfaces.IndexRepository
}

func (uc SourcesWithOpenParts) Execute(ctx context.Context) ([]partialentities.PartialSource, error) {
	srcs, err := uc.Index.SourcesWithOpenParts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return out, nil
}

// SourcesWithUnclustered lists sources that have claims no cluster owns yet. It is what makes a
// failed association pass resumable: the next run picks up exactly these claims.
type SourcesWithUnclustered struct {
	Index interfaces.IndexRepository
}

func (uc SourcesWithUnclustered) Execute(ctx context.Context) ([]partialentities.PartialSource, error) {
	srcs, err := uc.Index.SourcesWithUnclustered(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return out, nil
}

func (uc SourcesWithoutAtoms) Execute(ctx context.Context) ([]partialentities.PartialSource, error) {
	srcs, err := uc.Index.SourcesWithoutAtoms(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return out, nil
}
