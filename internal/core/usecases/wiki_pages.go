package usecases

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// WikiPageInput is what a save receives: the human's title, tags and Markdown body.
type WikiPageInput struct {
	ID    ulid.ULID // zero on create
	Title string
	Tags  []string
	Body  string
}

// WikiPageResult is one saved page and the address of its source representation.
type WikiPageResult struct {
	Page    entities.WikiPage
	Hash    string
	Created bool
	// NeedsReset says an older derivation exists and must be cleared before the pump continues. The
	// caller does that in the background: a save must not wait for the claims and tree of the
	// previous text to be deleted.
	NeedsReset bool
	// URLsAdded are the page's links that no source knows yet. The caller fetches them in the
	// background: a save never waits for the network. A link that already has a source — even one
	// whose fetch failed — is only associated and does not appear here.
	URLsAdded []string
	// StaleSourceHash is an older content-addressed representation the page pointed at before the
	// stable WikiHash. The caller removes it in the background, like a link that left.
	StaleSourceHash string
	// Retag says the page's own text (title or body) changed, so its derived tags are stale. A save
	// that only moved a tag must not spend a model call on re-tagging what it did not touch.
	Retag bool
}

// WikiPageSummary is the list shape: no body, but everything a list needs.
type WikiPageSummary struct {
	ID    ulid.ULID
	Title string
	// Tags are the human's labels; AutoTags are the derived ones. Both are shown, and a filter
	// matches either.
	Tags       []string
	AutoTags   []string
	SourceHash string
	// FavoriteAt is the reader's star on the page.
	FavoriteAt time.Time
	UpdatedAt  time.Time
}

// WikiTagCount is one tag and how many pages carry it.
type WikiTagCount struct {
	Tag   string
	Count int
}

// WikiList is the wiki lens: the pages and the tag cloud.
type WikiList struct {
	Pages []WikiPageSummary
	Tags  []WikiTagCount
}

// ListWikiPages scans the page files. It is deliberately not an index query: the page file is the
// truth, and a wiki with a few hundred pages is cheaper to read than another cache to keep honest.
type ListWikiPages struct {
	Pages interfaces.WikiStore
}

func (uc ListWikiPages) Execute(ctx context.Context) (WikiList, error) {
	pages, err := uc.Pages.List(ctx)
	if err != nil {
		return WikiList{}, err
	}
	out := WikiList{Pages: make([]WikiPageSummary, 0, len(pages))}
	counts := map[string]int{}
	for _, p := range pages {
		out.Pages = append(out.Pages, WikiPageSummary{
			ID: p.ID, Title: p.Title, Tags: p.Tags, AutoTags: p.AutoTags,
			SourceHash: p.SourceHash, FavoriteAt: p.FavoriteAt, UpdatedAt: p.UpdatedAt,
		})
		// The cloud counts BOTH kinds: a reader filters by the tag, not by who wrote it.
		for _, tag := range p.AllTags() {
			counts[tag]++
		}
	}
	out.Tags = make([]WikiTagCount, 0, len(counts))
	for tag, n := range counts {
		out.Tags = append(out.Tags, WikiTagCount{Tag: tag, Count: n})
	}
	sort.SliceStable(out.Tags, func(i, j int) bool {
		if out.Tags[i].Count != out.Tags[j].Count {
			return out.Tags[i].Count > out.Tags[j].Count
		}
		return out.Tags[i].Tag < out.Tags[j].Tag
	})
	return out, nil
}

// GetWikiPage reads one page by ID.
type GetWikiPage struct {
	Pages interfaces.WikiStore
}

func (uc GetWikiPage) Execute(ctx context.Context, id ulid.ULID) (entities.WikiPage, error) {
	page, err := uc.Pages.Read(ctx, id)
	if err != nil {
		if errors.Is(err, types.ErrNotFound) {
			return entities.WikiPage{}, err
		}
		return entities.WikiPage{}, types.New("not_found", "no such wiki page", types.ErrNotFound)
	}
	return page, nil
}

// SaveWikiPage writes the page file and its source representation.
//
// The source has a STABLE address (entities.WikiHash), so an edit reprocesses the same source from
// capture instead of adding a second source. The old derivation is cleared first: the tree and the
// claims describe the old text, and a page edit is a new document, not a correction of a node.
//
// NOTHING IS FETCHED AND NOTHING IS DELETED HERE. A page with a hundred links used to fetch every new
// link inside the request; fetching moves to the caller's queue (WikiPageResult.URLsAdded). A link
// that LEFT the page used to be deleted inside the request too, and that delete walked the whole atom
// vault — one save took 73 seconds for three removed links. The page file is the new truth; the
// caller runs PruneWikiLinks in the background after every save, which reconciles the sources the
// page owns with what it declares.
type SaveWikiPage struct {
	Pages     interfaces.WikiStore
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Steps     interfaces.StepTracker
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	Log       *slog.Logger
}

func (uc SaveWikiPage) Execute(ctx context.Context, in WikiPageInput) (WikiPageResult, error) {
	now := uc.Clock.Now()
	id := in.ID
	created := false
	if id == (ulid.ULID{}) {
		id = ulid.Make()
		created = true
	}
	old, oldErr := uc.Pages.Read(ctx, id)
	if oldErr != nil {
		created = true
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = wikiTitleFromBody(in.Body)
	}
	if title == "" {
		title = "Untitled"
	}
	body := strings.TrimRight(in.Body, "\n")
	urls := coremarkdown.URLs(body)
	tags := make([]string, 0, len(in.Tags))
	seen := map[string]bool{}
	for _, tag := range in.Tags {
		if t := coremarkdown.NormalizeTag(tag); t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	page := entities.WikiPage{
		ID: id, Title: title, Tags: tags, URLs: urls, Body: body,
		CreatedAt: now, UpdatedAt: now,
	}
	if !created {
		page.CreatedAt = old.CreatedAt
		// The star is the reader's, not the editor's: an edit that never mentions it must not clear it.
		page.FavoriteAt = old.FavoriteAt
		// The DERIVED tags belong to the page, not to this form: the editor sends the human's tags only,
		// and a save that forgot the derived ones would erase them every time somebody edits a typo.
		page.AutoTags = old.AutoTags
	}
	hash := entities.WikiHash(id)
	page.SourceHash = hash

	content := wikiSourceMarkdown(title, body)
	res := WikiPageResult{Page: page, Hash: hash, Created: created}
	res.Retag = created || oldErr != nil || old.Title != title || old.Body != body
	res.URLsAdded = uc.newLinks(ctx, page)
	// A tag-only (or URL-only) edit changes the page, not the prose source. Do not throw a good
	// derivation away for frontmatter or links that the document source does not need re-deriving.
	sameContent := !created && old.SourceHash == hash && wikiSourceMarkdown(old.Title, old.Body) == content
	if sameContent {
		if _, err := uc.Index.GetSourceByHash(ctx, hash); err == nil {
			page.SourceHash = hash
			if err := uc.Pages.Write(ctx, page); err != nil {
				return WikiPageResult{}, err
			}
			return res, nil
		}
	}
	obj, err := uc.Store.PutAt(hash, ".md", []byte(content))
	if err != nil {
		return WikiPageResult{}, err
	}

	existing, getErr := uc.Index.GetSourceByHash(ctx, hash)
	if getErr == nil {
		// The old derivation is cleared in the background before the pipeline continues. Deleting
		// claims and tree rows here made a save wait for work that is not part of writing the page.
		res.NeedsReset = true
	} else {
		existing.ID = ulid.Make()
		existing.ImportedAt = now
	}
	// A page from an older scheme may point at a content-addressed source. That source must not
	// linger beside the stable one: the page owns exactly one current representation. It is removed
	// in the background — checking that it exists is an index read, deleting it is vault work.
	if old.SourceHash != "" && old.SourceHash != hash {
		if _, err := uc.Index.GetSourceByHash(ctx, old.SourceHash); err == nil {
			res.StaleSourceHash = old.SourceHash
		}
	}

	src := entities.Source{
		ID: existing.ID, Hash: hash, Kind: entities.SourceKindMD, Ext: ".md", Path: obj.Path,
		Title: title, OriginalName: entities.WikiPrefix + id.String(),
		Bytes: obj.Bytes, TreeState: entities.TreeNone,
		ImportedAt: existing.ImportedAt, Meta: "{}",
	}
	if src.ImportedAt.IsZero() {
		src.ImportedAt = now
	}
	if err := uc.Index.UpsertSource(ctx, src); err != nil {
		return WikiPageResult{}, err
	}
	if uc.Steps != nil {
		if err := uc.Steps.SetIdentity(hash, entities.SourceIdentity{
			Kind: entities.SourceKindMD, OriginalName: entities.WikiPrefix + id.String(),
		}); err != nil && uc.Log != nil {
			uc.Log.Warn("wiki: source identity not recorded", "hash", hash, "err", err)
		}
		_ = uc.Steps.SetTitle(hash, title)
		// The save is step 1 again: the pump will capture the new bytes, then outline and distill.
		if err := uc.Steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{
			State: entities.StepPending, At: now, Detail: "wiki page changed",
		}); err != nil && uc.Log != nil {
			uc.Log.Warn("wiki: capture not re-armed", "hash", hash, "err", err)
		}
	}
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: now, Phase: "wiki", Level: "info", Message: "wiki page saved",
			Fields: map[string]any{"page": id.String(), "title": title, "bytes": len(content)},
		})
	}
	if err := uc.Pages.Write(ctx, page); err != nil {
		return WikiPageResult{}, err
	}
	res.Page = page
	return res, nil
}

// newLinks is the page's links that no source knows yet. The caller queues the fetches; the save
// itself touches no network. Existence is by URL KEY (trailing slash and fragment ignored), and an
// existing URL source — even a failed fetch — is only associated, never refetched.
//
// ONE index read, not one per link: FindURLSource lists every URL source on each call, and a page with
// 63 links paid that whole list 63 times inside the save.
func (uc SaveWikiPage) newLinks(ctx context.Context, page entities.WikiPage) []string {
	if len(page.URLs) == 0 {
		return nil
	}
	known := urlSourceKeys(ctx, uc.Index)
	added := make([]string, 0, len(page.URLs))
	for _, raw := range page.URLs {
		if !known[entities.URLKey(raw)] {
			added = append(added, raw)
		}
	}
	return added
}

// urlSourceKeys is the set of URL KEYS the index knows, from ONE read. A read failure answers "nothing
// is known": the link is then reported as new, and the queue skips it after its own lookup.
func urlSourceKeys(ctx context.Context, index interfaces.IndexRepository) map[string]bool {
	keys := map[string]bool{}
	if index == nil {
		return keys
	}
	rows, err := index.ListURLSources(ctx)
	if err != nil {
		return keys
	}
	for _, src := range rows {
		if src.URL != "" {
			keys[entities.URLKey(src.URL)] = true
		}
	}
	return keys
}

// pageURLs is the link set a page DECLARES. A page written before URL tracking has no frontmatter list;
// its body is still the declaration.
func pageURLs(page entities.WikiPage) []string {
	if len(page.URLs) > 0 {
		return page.URLs
	}
	return coremarkdown.URLs(page.Body)
}

// pageURLKeys is pageURLs as a set of URL KEYS.
func pageURLKeys(page entities.WikiPage) map[string]bool {
	urls := pageURLs(page)
	keys := make(map[string]bool, len(urls))
	for _, raw := range urls {
		keys[entities.URLKey(raw)] = true
	}
	return keys
}

// otherPagesURLKeys is the set of URL KEYS every OTHER page still declares, and whether it could be
// read. A read error is conservative: not knowing is a reason to leave a source alone, never to
// delete one.
func otherPagesURLKeys(ctx context.Context, pages interfaces.WikiStore, pageID ulid.ULID) (map[string]bool, bool) {
	if pages == nil {
		return nil, false
	}
	all, err := pages.List(ctx)
	if err != nil {
		return nil, false
	}
	refs := map[string]bool{}
	for _, other := range all {
		if other.ID == pageID {
			continue
		}
		for _, raw := range pageURLs(other) {
			refs[entities.URLKey(raw)] = true
		}
	}
	return refs, true
}

// PruneWikiLinks removes what a page stopped owning: the URL sources it brought in and no longer
// declares, plus an older representation it pointed at before the stable WikiHash.
//
// It runs OFF the request path and inside the vault gate, after EVERY save, and RE-READS the page
// instead of trusting a diff. That makes it idempotent and race-safe (a link the reader re-added
// before the job ran is declared again, so its source stays) and it heals what a diff could not: a
// crash between the page file and the cleanup, or a page edited outside the app.
type PruneWikiLinks struct {
	Pages  interfaces.WikiStore
	Index  interfaces.IndexRepository
	Delete DeleteSource
	Log    *slog.Logger
}

// PruneWikiLinksResult reports what the pass removed.
type PruneWikiLinksResult struct {
	LinksRemoved int
	StaleRemoved bool
}

// Execute prunes one page's orphaned link sources. A page that is already gone is a no-op: its own
// delete removed the links with it.
func (uc PruneWikiLinks) Execute(ctx context.Context, pageID ulid.ULID, staleHash string) (PruneWikiLinksResult, error) {
	var res PruneWikiLinksResult
	if staleHash != "" {
		if _, err := uc.Delete.ExecuteOwned(ctx, staleHash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("wiki: old representation not removed", "page", pageID.String(), "hash", staleHash, "err", err)
			}
		} else {
			res.StaleRemoved = true
		}
	}
	page, err := uc.Pages.Read(ctx, pageID)
	if err != nil {
		return res, nil
	}
	declared := pageURLKeys(page)
	children, err := uc.Index.ListSourceChildren(ctx, "", pageID.String())
	if err != nil {
		return res, err
	}
	candidates := make([]entities.Source, 0, len(children))
	for _, child := range children {
		if child.Origin != entities.WikiLinkOrigin || declared[entities.URLKey(child.URL)] {
			continue
		}
		candidates = append(candidates, child)
	}
	if len(candidates) == 0 {
		return res, nil
	}
	// One page list for the whole pass, however many links left.
	refs, ok := otherPagesURLKeys(ctx, uc.Pages, pageID)
	if !ok {
		return res, nil
	}
	for _, child := range candidates {
		if refs[entities.URLKey(child.URL)] {
			continue
		}
		if _, err := uc.Delete.ExecuteOwned(ctx, child.Hash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("wiki: URL source not removed", "page", pageID.String(), "url", child.URL, "err", err)
			}
			continue
		}
		res.LinksRemoved++
	}
	return res, nil
}

// DeleteWikiPage removes the page and the source representation it owns.
type DeleteWikiPage struct {
	Pages  interfaces.WikiStore
	Index  interfaces.IndexRepository
	Delete DeleteSource
	Log    *slog.Logger
}

func (uc DeleteWikiPage) Execute(ctx context.Context, id ulid.ULID) (string, []string, error) {
	page, err := uc.Pages.Read(ctx, id)
	if err != nil {
		return "", nil, types.New("not_found", "no such wiki page", types.ErrNotFound)
	}
	var removedURLs []string
	// The page's OWN link sources, from the index: one query, not one URL list per declared link.
	children, err := uc.Index.ListSourceChildren(ctx, "", id.String())
	if err != nil && uc.Log != nil {
		uc.Log.Warn("wiki: link sources not listed", "page", id.String(), "err", err)
	}
	// A read error here is conservative: not knowing what other pages declare is a reason to leave
	// their link sources alone, never to delete one.
	refs, refsOK := otherPagesURLKeys(ctx, uc.Pages, page.ID)
	for _, child := range children {
		if child.Origin != entities.WikiLinkOrigin {
			continue
		}
		if refsOK && refs[entities.URLKey(child.URL)] {
			continue
		}
		if _, err := uc.Delete.ExecuteOwned(ctx, child.Hash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("wiki: URL source not removed with the page", "page", page.ID.String(), "url", child.URL, "err", err)
			}
			continue
		}
		removedURLs = append(removedURLs, child.Hash)
	}
	if page.SourceHash != "" {
		if _, err := uc.Index.GetSourceByHash(ctx, page.SourceHash); err == nil {
			if _, err := uc.Delete.ExecuteOwned(ctx, page.SourceHash); err != nil {
				return page.SourceHash, removedURLs, err
			}
		}
	}
	if err := uc.Pages.Remove(ctx, id); err != nil {
		return page.SourceHash, removedURLs, err
	}
	return page.SourceHash, removedURLs, nil
}

func wikiTitleFromBody(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			if title := strings.TrimSpace(strings.TrimLeft(t, "#")); title != "" {
				return title
			}
		}
	}
	return ""
}

func wikiSourceMarkdown(title, body string) string {
	body = strings.TrimSpace(body)
	heading := "# " + title
	if strings.HasPrefix(body, heading) {
		return body + "\n"
	}
	if body == "" {
		return heading + "\n"
	}
	return heading + "\n\n" + body + "\n"
}
