package usecases

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// A LISTING is a page that declares articles instead of carrying one: a blog index, a newsletter
// archive. It is the third kind of list beside a sitemap (complete, reconciled) and a feed (a
// window). Like a feed it is a window — a page that rotates out of the pagination is KEPT — and
// unlike both it is never discovered automatically: the links are a guess about a page's layout, so
// the human confirms the listing before it becomes an owner.

// ListingChoice is what the caller wants done with a page that looks like a listing.
type ListingChoice int

const (
	// ListingAuto detects a listing and OFFERS it: a new page is stored as a collected link and the
	// answer carries how many articles the page declares. Nothing is reclassified behind the
	// reader's back, and a card grid is never distilled into claims about its own titles.
	ListingAuto ListingChoice = iota
	// ListingYes turns the page into a listing (the human confirmed it, or the API forced it).
	ListingYes
	// ListingNo never treats the page as a listing.
	ListingNo
)

// ListingPage is one page of a listing as a pagination walk read it.
type ListingPage struct {
	Items  []listing.Item
	Next   string
	Status int
}

// QueueListing claims the articles a listing declared. Their owner is the listing source itself, so
// removing the listing removes every article that exists only because of it.
func (uc URLs) QueueListing(ctx context.Context, parentHash string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.ListingOrigin, ParentHash: parentHash}, urls)
}

// MarkFailedListing records a failure for an article that a listing owns.
func (uc URLs) MarkFailedListing(ctx context.Context, rawURL, parentHash string, cause error) (string, error) {
	return uc.MarkFailedOwned(ctx, rawURL, urlOwnership{Origin: entities.ListingOrigin, ParentHash: parentHash}, cause)
}

// listingResult reads a page's links and applies the listing rules. It never decides whether the
// page IS a listing: that needs the readable text, which the caller measured (see listing.Suggests).
func (uc URLs) listingResult(ctx context.Context, body []byte, pageURL string) (listing.Result, error) {
	if uc.Links == nil {
		return listing.Result{}, nil
	}
	links, err := uc.Links.Links(ctx, body, pageURL)
	if err != nil {
		return listing.Result{}, err
	}
	return listing.Detect(pageURL, links), nil
}

// renderedListing re-reads a page's cards from its rendered DOM. It is the browser the app already
// carries, used for the one thing only a browser can see: a listing a script builds. The plain
// reading stands when there is no browser, when the page is not HTML, or when the render fails —
// detection must never be a reason to lose a page.
func (uc URLs) renderedListing(ctx context.Context, rawURL string, plain listing.Result) (listing.Result, bool) {
	if uc.Render == nil {
		return plain, false
	}
	rendered, err := uc.Render.Render(ctx, rawURL)
	if err != nil {
		if uc.Log != nil {
			uc.Log.Warn("listing: the page could not be rendered — the plain reading stands",
				"url", rawURL, "err", err)
		}
		return plain, false
	}
	if rendered.Wall.Blocks() || !looksLikeHTML(rendered.ContentType) {
		return plain, false
	}
	res, err := uc.listingResult(ctx, rendered.HTML, rawURL)
	if err != nil {
		return plain, false
	}
	if uc.Log != nil {
		uc.Log.Debug("listing: the cards were read from the rendered DOM",
			"url", rawURL, "articles", len(res.Items), "plain_articles", len(plain.Items))
	}
	return res, true
}

// linkedInMain counts how many candidates the readable text contains. A card grid that readability
// kept is the page's content; a related rail it dropped is not.
//
// A card's TITLE counts as much as its address. Readability drops an overlay anchor (it carries no
// text) but keeps the heading the card renders, so a page that is nothing but 17 card titles carries
// no URL at all — and is still exactly its titles.
func linkedInMain(items []listing.Item, markdown string) int {
	if markdown == "" {
		return 0
	}
	// An escaped character in the Markdown (`\*`, `\_`) is the same word as the card's title.
	plain := strings.ReplaceAll(markdown, `\`, "")
	n := 0
	for _, item := range items {
		if strings.Contains(markdown, item.URL) || titleInText(plain, item.Title) {
			n++
		}
	}
	return n
}

// titleProbe is how much of a title has to appear in the readable text to count it. A prefix is
// enough: a card's title is truncated to the card's width, and a heading carries the whole one.
const titleProbe = 80

// titleInText reports whether the readable text carries the item's title. Short titles are not
// evidence of anything (a two-word heading matches half a page), so a minimum length applies.
func titleInText(text, title string) bool {
	probe := strings.TrimSuffix(truncateRunes(title, titleProbe), "…")
	if utf8.RuneCountInString(probe) < 12 {
		return false
	}
	return textmatch.Contains(text, probe)
}

// truncateRunes keeps the leading runes of a string, never splitting one.
func truncateRunes(s string, limit int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// storeListing writes the page as a source of its own and returns the articles it declared. The
// caller claims those URLs with this source as their owner; the pagination walk follows `Next`.
//
// A listing is not a bookmark and not a document: it is the parent of the articles it lists. Its
// body is the list (rendered from the cards, because readability throws a card grid away), so
// outline and distill are recorded as skipped.
func (uc URLs) storeListing(
	ctx context.Context,
	rawURL string,
	page interfaces.FetchedPage,
	res listing.Result,
	declaredTitle string,
	existing entities.Source,
	known bool,
) (partialentities.PartialSource, error) {
	now := uc.Clock.Now()
	title := firstNonEmpty(strings.TrimSpace(declaredTitle), listingTitle(rawURL))
	// A listing that is re-read merges page 1 into the articles it already declared: its snapshot is
	// the archive, not the newest page. A page that becomes a listing has nothing to merge yet.
	items := res.Items
	if known {
		items = mergeListingItems(res.Items, uc.listingSnapshotItems(existing))
	}
	snapshot := renderListingSnapshot(rawURL, title, page.Status, now, items)
	contentHash := contentHashOf(title, snapshot)

	obj, err := uc.Store.PutAt(entities.URLHash(rawURL), ".md", []byte(snapshot))
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	src := entities.Source{
		ID: ulid.Make(), Hash: obj.Hash, Kind: entities.SourceKindURL, Ext: obj.Ext, Path: obj.Path,
		URL: rawURL, Title: title, OriginalName: rawURL, Listing: true,
		Bytes: obj.Bytes, TreeState: entities.TreeNone, ImportedAt: now,
		Meta: metaJSON(urlMeta{ContentHash: contentHash, URL: rawURL}),
	}
	resetNeeded := false
	if known {
		src.ID, src.Hash = existing.ID, existing.Hash
		src.ImportedAt = existing.ImportedAt
		// Ownership outlives the snapshot: a listing that a wiki page or another list brought in
		// keeps its owner when its own body is fetched.
		src.Origin, src.WikiPage = existing.Origin, existing.WikiPage
		src.ParentHash = existing.ParentHash
		// The page was a document (or a bookmark) until now. Whatever was derived from it belongs
		// to other text and must go: the caller resets through the app's exclusive path.
		resetNeeded = !existing.Listing && existing.Path != ""
		if src.Hash != obj.Hash {
			moved, mErr := uc.Store.PutAt(src.Hash, ".md", []byte(snapshot))
			if mErr != nil {
				return partialentities.PartialSource{}, mErr
			}
			src.Ext, src.Path, src.Bytes = moved.Ext, moved.Path, moved.Bytes
		}
		if uc.Steps != nil {
			_ = uc.Steps.Reset(src.Hash) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		}
	}
	if known {
		if err := uc.Index.FillURLSource(ctx, src); err != nil {
			return partialentities.PartialSource{}, err
		}
	} else if err := uc.Index.UpsertSource(ctx, src); err != nil {
		return partialentities.PartialSource{}, err
	}
	_ = uc.Index.SetSourceURLState(ctx, src.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK,
		fmt.Sprintf("HTTP %d · listing · %d articles", page.Status, len(items)), nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "listing", nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "listing", nil)
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
			Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash,
			Listing: true, ContentHash: contentHash,
		})
	}
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "info", "listing expanded", map[string]any{
		"url": rawURL, "articles": len(items), "next": res.Next,
	})
	stored, err := uc.Index.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		stored = src
	}
	out := partialentities.PartialFromSource(stored)
	out.ListingURLs = itemURLs(items)
	out.ListingItems = items
	// The pagination is walked ONCE, when the listing is captured. Re-reading an existing listing is
	// a refresh: page 1 is enough, and the caller must not walk the archive again.
	if !existing.Listing {
		out.ListingNext = res.Next
	}
	out.NeedsReset = resetNeeded && !out.NeedsReset
	return out, nil
}

// ExpandListing reads ONE further page of a listing's pagination. Nothing is written here: the walk
// accumulates the pages and FinishListing writes the listing once, so an interrupted walk leaves
// the source with the items it already had.
func (uc URLs) ExpandListing(ctx context.Context, hash, pageURL string) (ListingPage, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return ListingPage{}, err
	}
	if !src.Listing {
		return ListingPage{}, fmt.Errorf("source %s is not a listing", hash)
	}
	page, err := uc.Fetch.Fetch(ctx, pageURL)
	if err != nil {
		return ListingPage{}, err
	}
	if page.Wall.Blocks() {
		return ListingPage{}, fmt.Errorf("the listing page is blocked: %s", page.Wall.Label())
	}
	if page.Status < 200 || page.Status >= 300 {
		return ListingPage{}, fmt.Errorf("the listing page answered HTTP %d", page.Status)
	}
	// The page's own address, not the listing's: relative links resolve against the page that was
	// fetched, and the pager's "not the page itself" check compares the right address.
	res, err := uc.listingResult(ctx, page.Body, pageURL)
	if err != nil {
		return ListingPage{}, err
	}
	// A page whose plain response declares no cards — or no next page — may be a list a script
	// builds. The browser is the only reader that can see one.
	if len(res.Items) == 0 || res.Next == "" {
		if rendered, ok := uc.renderedListing(ctx, pageURL, res); ok {
			res = rendered
		}
	}
	return ListingPage{Items: res.Items, Next: res.Next, Status: page.Status}, nil
}

// FinishListing rewrites the listing's snapshot with every article the walk has seen so far. It is
// called after each page (a crash keeps what was found) and returns how many unique articles the
// listing declares.
func (uc URLs) FinishListing(ctx context.Context, hash string, items []listing.Item, status int) (int, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return 0, err
	}
	unique := dedupeItems(items)
	snapshot := renderListingSnapshot(src.URL, src.Title, status, uc.Clock.Now(), unique)
	obj, err := uc.Store.PutAt(src.Hash, ".md", []byte(snapshot))
	if err != nil {
		return 0, err
	}
	src.Ext, src.Path, src.Bytes = obj.Ext, obj.Path, obj.Bytes
	src.Meta = metaWithIdentity(metaWith(src.Meta, map[string]any{
		"url": src.URL, "content_hash": contentHashOf(src.Title, snapshot), "last_error": nil, "wall": nil,
	}), src)
	if err := uc.Index.FillURLSource(ctx, src); err != nil {
		return 0, err
	}
	_ = uc.Index.SetSourceMeta(ctx, src.Hash, src.Meta) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	if status > 0 {
		_ = uc.Index.SetSourceURLState(ctx, src.Hash, status, uc.Clock.Now()) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	}
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK,
		fmt.Sprintf("listing · %d articles", len(unique)), nil)
	return len(unique), nil
}

// renderListingSnapshot is what the listing source shows: its address, how many articles it
// declares, and the list with the titles the cards offered. The articles are separate sources.
func renderListingSnapshot(rawURL, title string, status int, fetchedAt time.Time, items []listing.Item) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", fetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	b.WriteString("listing: true\n")
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "%d article%s declared by this listing.\n\n", len(items), pluralSuffix(len(items)))
	for _, item := range items {
		name := strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(item.Title)
		if item.Date == "" {
			fmt.Fprintf(&b, "- [%s](%s)\n", name, item.URL)
			continue
		}
		fmt.Fprintf(&b, "- [%s](%s) — %s\n", name, item.URL, item.Date)
	}
	return b.String()
}

// listingTitle names the listing when the page declares no usable title of its own.
func listingTitle(rawURL string) string {
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u.Host != "" {
		path := strings.TrimSuffix(u.Path, "/")
		if path != "" {
			return "Listing · " + u.Host + path
		}
		return "Listing · " + u.Host
	}
	return "Listing"
}

// listingSnapshotItems reads the articles the listing's snapshot already declares. A refresh only
// reads page 1, so without this the archive the initial walk collected would disappear from the
// listing's own page every time it is checked. The parse is of our own rendering, never of a page.
func (uc URLs) listingSnapshotItems(src entities.Source) []listing.Item {
	if uc.Store == nil || src.Hash == "" || src.Ext == "" {
		return nil
	}
	rc, err := uc.Store.Open(src.Hash, src.Ext)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return parseListingSnapshot(string(body))
}

var listingLine = regexp.MustCompile(`(?m)^- \[(.*?)\]\((\S+)\)(?: — (.*))?$`)

func parseListingSnapshot(body string) []listing.Item {
	matches := listingLine.FindAllStringSubmatch(string(snapshotBody([]byte(body))), -1)
	items := make([]listing.Item, 0, len(matches))
	for _, m := range matches {
		items = append(items, listing.Item{Title: m[1], URL: m[2], Date: strings.TrimSpace(m[3])})
	}
	return items
}

// mergeListingItems puts the freshly read page first (it is the newest) and keeps every article the
// listing declared before. A listing is a window: a page that rotated out of the pagination is kept.
func mergeListingItems(fresh, known []listing.Item) []listing.Item {
	seen := make(map[string]bool, len(fresh)+len(known))
	out := make([]listing.Item, 0, len(fresh)+len(known))
	for _, group := range [][]listing.Item{fresh, known} {
		for _, item := range group {
			key := entities.URLKey(item.URL)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, item)
		}
	}
	return out
}

// dedupeItems keeps the first occurrence of an address (trailing slash and fragment ignored), so a
// listing that repeats a featured post across pages declares it once.
func dedupeItems(items []listing.Item) []listing.Item {
	seen := make(map[string]bool, len(items))
	out := make([]listing.Item, 0, len(items))
	for _, item := range items {
		key := entities.URLKey(item.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func itemURLs(items []listing.Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.URL)
	}
	return out
}
