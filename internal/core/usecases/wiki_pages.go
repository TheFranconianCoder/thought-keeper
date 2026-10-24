package usecases

import (
	"context"
	"errors"
	"log/slog"
	"slices"
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
	// URLHashesRemoved are the stable addresses of the URL sources this save stopped owning.
	URLHashesRemoved []string
	URLsRemoved      int
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
			SourceHash: p.SourceHash, UpdatedAt: p.UpdatedAt,
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
// NOTHING IS FETCHED HERE. A page with a hundred links used to fetch every new link, one after the
// other, inside the request: minutes of saving for one page, and nothing written until the last link
// answered. The links a page declares are the caller's to queue (WikiPageResult.URLsAdded); writing the
// page is a local operation and stays one.
type SaveWikiPage struct {
	Pages     interfaces.WikiStore
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Steps     interfaces.StepTracker
	Delete    DeleteSource
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
		// The DERIVED tags belong to the page, not to this form: the editor sends the human's tags only,
		// and a save that forgot the derived ones would erase them every time somebody edits a typo.
		page.AutoTags = old.AutoTags
	}
	hash := entities.WikiHash(id)
	page.SourceHash = hash

	content := wikiSourceMarkdown(title, body)
	res := WikiPageResult{Page: page, Hash: hash, Created: created}
	res.Retag = created || oldErr != nil || old.Title != title || old.Body != body
	res.URLsAdded, res.URLHashesRemoved = uc.planURLs(ctx, page, old)
	res.URLsRemoved = len(res.URLHashesRemoved)
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
	// linger beside the stable one: the page owns exactly one current representation.
	if old.SourceHash != "" && old.SourceHash != hash {
		if _, err := uc.Index.GetSourceByHash(ctx, old.SourceHash); err == nil {
			_, _ = uc.Delete.ExecuteOwned(ctx, old.SourceHash)
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

// planURLs compares the page's URL set with the previous file, and nothing more. A URL that arrived is
// only REPORTED (the caller queues the fetch); a URL that left is removed only if a wiki page created
// its source and no other page still references it. The page body is not re-parsed for the old URLs,
// and an existing URL source is never touched by a save — not refetched, and not by a link that merely
// stays on the page.
func (uc SaveWikiPage) planURLs(ctx context.Context, page, old entities.WikiPage) (added, removed []string) {
	for _, raw := range page.URLs {
		if _, known := FindURLSource(ctx, uc.Index, raw); known {
			continue
		}
		added = append(added, raw)
	}

	// The comparison is by URL KEY (trailing slash and fragment ignored): renaming "/a" to "/a/" is
	// not a link that left the page, it is the same link.
	newSet := make(map[string]bool, len(page.URLs))
	for _, raw := range page.URLs {
		newSet[entities.URLKey(raw)] = true
	}
	oldURLs := old.URLs
	if len(oldURLs) == 0 && old.Body != "" {
		oldURLs = coremarkdown.URLs(old.Body)
	}
	var gone []string
	for _, raw := range oldURLs {
		if !newSet[entities.URLKey(raw)] {
			gone = append(gone, raw)
		}
	}
	if len(gone) == 0 {
		return added, nil
	}
	// One page list for the whole save, however many links leave it. A read error is conservative:
	// not knowing is a reason to leave a source alone, never to delete one.
	refs, ok := uc.referencesOfOthers(ctx, page.ID)
	if !ok {
		return added, nil
	}
	for _, raw := range gone {
		if refs[entities.URLKey(raw)] {
			continue
		}
		src, known := FindURLSource(ctx, uc.Index, raw)
		if !known || src.Origin != entities.WikiLinkOrigin {
			continue
		}
		if _, err := uc.Delete.ExecuteOwned(ctx, src.Hash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("wiki: URL source not removed", "page", page.ID.String(), "url", raw, "err", err)
			}
			continue
		}
		removed = append(removed, src.Hash)
	}
	return added, removed
}

// referencesOfOthers is the set of URLs every OTHER page still claims, and whether it could be read.
func (uc SaveWikiPage) referencesOfOthers(ctx context.Context, pageID ulid.ULID) (map[string]bool, bool) {
	if uc.Pages == nil {
		return nil, false
	}
	pages, err := uc.Pages.List(ctx)
	if err != nil {
		return nil, false
	}
	refs := map[string]bool{}
	for _, other := range pages {
		if other.ID == pageID {
			continue
		}
		urls := other.URLs
		if len(urls) == 0 {
			// A page written before URL tracking has no frontmatter list; its body is still the
			// declaration. Parse it here rather than deleting a source another page still links.
			urls = coremarkdown.URLs(other.Body)
		}
		for _, raw := range urls {
			refs[entities.URLKey(raw)] = true
		}
	}
	return refs, true
}

// DeleteWikiPage removes the page and the source representation it owns.
type DeleteWikiPage struct {
	Pages  interfaces.WikiStore
	Index  interfaces.IndexRepository
	Delete DeleteSource
	Log    *slog.Logger
}

func (uc DeleteWikiPage) referencedElsewhere(ctx context.Context, pageID ulid.ULID, rawURL string) bool {
	if uc.Pages == nil {
		return false
	}
	pages, err := uc.Pages.List(ctx)
	if err != nil {
		return true
	}
	for _, page := range pages {
		if page.ID == pageID {
			continue
		}
		urls := page.URLs
		if len(urls) == 0 {
			// A page written before URL tracking has no frontmatter list; its body is still the
			// declaration. Parse it here rather than deleting a source another page still links.
			urls = coremarkdown.URLs(page.Body)
		}
		if slices.ContainsFunc(urls, func(raw string) bool { return entities.URLKey(raw) == entities.URLKey(rawURL) }) {
			return true
		}
	}
	return false
}

func (uc DeleteWikiPage) Execute(ctx context.Context, id ulid.ULID) (string, []string, error) {
	page, err := uc.Pages.Read(ctx, id)
	if err != nil {
		return "", nil, types.New("not_found", "no such wiki page", types.ErrNotFound)
	}
	var removedURLs []string
	pageURLs := page.URLs
	if len(pageURLs) == 0 && page.Body != "" {
		pageURLs = coremarkdown.URLs(page.Body)
	}
	for _, raw := range pageURLs {
		if uc.referencedElsewhere(ctx, page.ID, raw) {
			continue
		}
		src, known := FindURLSource(ctx, uc.Index, raw)
		if !known || src.Origin != entities.WikiLinkOrigin {
			continue
		}
		if _, err := uc.Delete.ExecuteOwned(ctx, src.Hash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("wiki: URL source not removed with the page", "page", page.ID.String(), "url", raw, "err", err)
			}
			continue
		}
		removedURLs = append(removedURLs, src.Hash)
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
