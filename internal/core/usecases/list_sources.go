package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ListSources lists indexed sources, optionally filtered by kind.
type ListSources struct {
	Index interfaces.IndexRepository
}

// Execute returns sources ordered by import time (newest first).
func (uc ListSources) Execute(ctx context.Context, kind string, limit, offset int) ([]partialentities.PartialSource, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	srcs, err := uc.Index.ListSources(ctx, kind, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialSource, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, partialentities.PartialFromSource(s))
	}
	return out, nil
}

// Count is how many sources the list has in total: a page needs it to know whether it is the
// whole list.
func (uc ListSources) Count(ctx context.Context, kind string) (int, error) {
	return uc.Index.CountSources(ctx, kind)
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
