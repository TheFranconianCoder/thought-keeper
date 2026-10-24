package interfaces

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// IndexRepository is the derived index. Everything here is rebuildable from the
// vault with reindex.
type IndexRepository interface {
	UpsertSource(ctx context.Context, s entities.Source) error
	// FillURLSource completes a QUEUED url source (a link whose fetch was owed): the row existed
	// first, and the fetch writes its title, snapshot metadata and bookmark decision into it.
	FillURLSource(ctx context.Context, s entities.Source) error
	GetSourceByHash(ctx context.Context, hash string) (entities.Source, error)
	ListSources(ctx context.Context, kind string, limit, offset int) ([]entities.Source, error)
	// CountSources is the total behind a page, so the list knows whether there is more.
	CountSources(ctx context.Context, kind string) (int, error)
	// ListSourceChildren returns the sources one source owns: a sitemap's pages (parent hash) and a
	// wiki page's linked URLs (page id). The two sets are disjoint, one owner is set per child.
	ListSourceChildren(ctx context.Context, parentHash, wikiPage string) ([]entities.Source, error)
	// CountSourceChildren is how many sub-sources a source owns — what decides whether the detail
	// view shows the tab at all.
	CountSourceChildren(ctx context.Context, parentHash, wikiPage string) (int, error)
	DeleteSourceByHash(ctx context.Context, hash string) error
	SourceHashes(ctx context.Context) ([]string, error)
	// SetSourceTags mirrors a source's human labels into the index. The .meta record is the truth.
	SetSourceTags(ctx context.Context, hash string, tags []string) error
	// SetSourceAutoTags mirrors the agent's derived labels and their derivation time.
	SetSourceAutoTags(ctx context.Context, hash string, tags []string, at time.Time) error
	// SourceTagCounts is the observed source-tag catalog for the vocabulary.
	SourceTagCounts(ctx context.Context) (map[string]int, error)
	// SourceKinds returns every indexed source's kind, keyed by hash: the reindex uses it when a
	// source's record has no identity yet (an older vault), because the file name cannot tell a URL
	// snapshot from a Markdown source.
	SourceKinds(ctx context.Context) (map[string]string, error)

	// ResetSourceDerived restores the derived fields (title, pages) to the intake state.
	ResetSourceDerived(ctx context.Context, hash, title string) error
	// SetSourceTree records the tree state and refreshes derived source fields.
	SetSourceTree(ctx context.Context, hash, state, title string, pages int) error
	// SetSourceSteps mirrors a source's <hash>.meta file into the index. The file is the truth;
	// this is what lets the list view show the four lights without reading a file per row.
	SetSourceSteps(ctx context.Context, hash string, steps entities.StepSet) error
	// SetSourceKindURL marks a source as a URL snapshot and records its address (the repair for an
	// index that lost the fact).
	SetSourceKindURL(ctx context.Context, hash, url string) error
	// SetSourceTitle records the document's title (an empty title is ignored: a good title is never
	// wiped).
	SetSourceTitle(ctx context.Context, hash, title string) error
	// SetSourceLanguage records the source's detected language in the index metadata.
	SetSourceLanguage(ctx context.Context, hash, language string) error
	// SetSourceOrigin records the ownership of a URL source in the index metadata: the wiki page
	// or the parent sitemap/feed that controls its removal.
	SetSourceOrigin(ctx context.Context, hash, origin, wikiPage, parentHash string) error
	// ClearSourceOrigin removes that ownership: the source is independent (a manual addition wins
	// over the page, sitemap or feed that claimed the URL first).
	ClearSourceOrigin(ctx context.Context, hash string) error
	// SourceLanguages returns the languages present in the vault, keyed by source hash. The search
	// uses the SET to choose its morphological rules; a missing language is simply not a hint.
	SourceLanguages(ctx context.Context) (map[string]string, error)
	// SetSourceMeta writes a source's metadata: the mirror of what the vault's record holds — a URL
	// snapshot's content hash and its icon above all. A reindex restores it from the record, which is
	// why the two must be able to disagree without losing anything.
	SetSourceMeta(ctx context.Context, hash, meta string) error
	// SetSourceHash moves a source to the address it should have had, keeping its ID: a URL source
	// whose address was derived from its first snapshot moves to its URL's hash.
	SetSourceHash(ctx context.Context, oldHash, newHash, path string) error
	// ReplaceSourceNodes swaps a source's node rows atomically.
	ReplaceSourceNodes(ctx context.Context, sourceID ulid.ULID, nodes []entities.SourceNode) error
	// ListSourceNodes returns a source's nodes in document order.
	ListSourceNodes(ctx context.Context, sourceID ulid.ULID) ([]entities.SourceNode, error)

	// SourceHasAtoms reports whether any atom cites this source (derived document
	// status: has it been derived?).
	SourceHasAtoms(ctx context.Context, hash string) (bool, error)
	// SourcesWithoutAtoms lists sources with a built tree but no atoms yet.
	SourcesWithoutAtoms(ctx context.Context) ([]entities.Source, error)
	// SourcesWithOpenParts lists sources whose derivation is unfinished: text parts without a
	// recorded `done`. A long book moves through many runs, and a restart must find it again.
	SourcesWithOpenParts(ctx context.Context) ([]entities.Source, error)
	// SourceOpenParts counts the text parts of ONE source that no `done` record covers. It is the
	// per-source form of SourcesWithOpenParts: the pipeline asks it only when a step record is
	// ambiguous (distill ok, no claim, no recorded parts).
	SourceOpenParts(ctx context.Context, hash string) (int, error)
	// SourcesWithUnclustered lists sources whose claims no cluster owns yet: the association
	// backlog a later run resumes.
	SourcesWithUnclustered(ctx context.Context) ([]entities.Source, error)
	// SourcesWithNoTree lists sources whose tree was never built.
	SourcesWithNoTree(ctx context.Context) ([]entities.Source, error)

	// SetSourceURLState records the last HTTP status and check time of a URL source.
	SetSourceURLState(ctx context.Context, hash string, status int, checkedAt time.Time) error
	// ListURLSources lists every URL snapshot (newest first), so the refresher can
	// re-fetch and the UI can show per-URL status.
	ListURLSources(ctx context.Context) ([]entities.Source, error)
}
