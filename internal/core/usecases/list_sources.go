package usecases

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Sources is the use case of the source layer: intake, the lists and the facets, the readable
// content, and the delete that cascades into everything derived from a source.
type Sources struct {
	Store        interfaces.SourceStore
	Pages        interfaces.WikiStore
	Index        interfaces.IndexRepository
	Atoms        interfaces.AtomRepository
	AtomStore    interfaces.AtomStore
	CommandStore interfaces.CommandStore
	Tags         interfaces.SourceTagStore
	Shelf        interfaces.ShelfCounter
	Favicons     interfaces.FaviconStore
	Trees        interfaces.TreeStore
	Convert      interfaces.HTMLConverter
	Extractor    interfaces.Extractor
	Gateway      interfaces.AgentGateway
	SourceLog    interfaces.SourceLog
	Steps        interfaces.StepTracker
	Languages    interfaces.LanguageDetector
	Runs         interfaces.RunRepository
	Reset        SourceResetter
	Clock        interfaces.Clock
	Log          *slog.Logger
	Model        string
	Language     string
	MaxBytes     int64
	MaxClaims    int
	MaxChars     int
	CallTimeout  time.Duration
}

// SourceLister lists one page of sources and the filtered total. A handler holds this value; nil
// means the feature is not wired.
type SourceLister func(ctx context.Context, filter entities.SourceFilter, limit, offset int) ([]partialentities.PartialSource, error)

// List returns one page of sources, ordered by import time (newest first), narrowed by filter.
func (uc Sources) List(ctx context.Context, filter entities.SourceFilter, limit, offset int) ([]partialentities.PartialSource, error) {
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

// SourceCounter counts the sources a filter matches — the total behind one page. A handler holds this
// value; nil means the list shows only what it loaded.
type SourceCounter func(ctx context.Context, filter entities.SourceFilter) (int, error)

// Count returns how many sources the filter matches.
func (uc Sources) Count(ctx context.Context, filter entities.SourceFilter) (int, error) {
	return uc.Index.CountSources(ctx, filter)
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

// SourceFacetLister returns the tag cloud and the attached count. A handler holds this value; nil
// means there are no facets.
type SourceFacetLister func(ctx context.Context) (SourceFacetsResult, error)

// Facets returns the tag cloud and the attached count.
func (uc Sources) Facets(ctx context.Context) (SourceFacetsResult, error) {
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

// SourceGetter returns one source as the detail view shows it. A handler holds this value; nil means
// the feature is not wired.
type SourceGetter func(ctx context.Context, hash string) (partialentities.PartialSource, error)

// Get returns one source.
func (uc Sources) Get(ctx context.Context, hash string) (partialentities.PartialSource, error) {
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
	// A list declares more than it OWNS: a URL another owner brought in first stays with that owner.
	// The number keeps the two apart (24 declared, 15 owned) instead of making the difference look
	// like a missing article.
	if src.OwnsChildren() {
		out.Declared = len(declaredURLs(uc.Store, src))
	}
	return out, nil
}

// SourceChildrenResult is a source's sub-sources as the wire sees them.
type SourceChildrenResult struct {
	Sources []partialentities.PartialSource
	Total   int
}

// SourceChildLister lists the sources one source owns. A handler holds this value; nil means the
// feature is not wired.
type SourceChildLister func(ctx context.Context, hash string) (SourceChildrenResult, error)

// Children lists the sources one source owns.
func (uc Sources) Children(ctx context.Context, hash string) (SourceChildrenResult, error) {
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

// HasAtoms reports whether a source produced any claim.
func (uc Sources) HasAtoms(ctx context.Context, hash string) (bool, error) {
	if !entities.IsSHA256Hex(hash) {
		return false, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	return uc.Index.SourceHasAtoms(ctx, hash)
}

// WithoutTree lists the sources whose outline is missing.
func (uc Sources) WithoutTree(ctx context.Context) ([]partialentities.PartialSource, error) {
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

// WithOpenParts lists the sources with undistilled parts.
func (uc Sources) WithOpenParts(ctx context.Context) ([]partialentities.PartialSource, error) {
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

// WithUnclustered lists the sources with unclustered claims.
func (uc Sources) WithUnclustered(ctx context.Context) ([]partialentities.PartialSource, error) {
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

// WithoutAtoms lists the sources that produced no claim.
func (uc Sources) WithoutAtoms(ctx context.Context) ([]partialentities.PartialSource, error) {
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
