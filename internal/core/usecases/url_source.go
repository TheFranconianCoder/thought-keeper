package usecases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// urlMeta is the free-form metadata stored on a URL snapshot. ContentHash is what
// the refresher compares to tell "changed" from "same page"; the rest lets a
// bookmark carry its metadata.
type urlMeta struct {
	ContentHash string       `json:"content_hash,omitempty"`
	LastError   string       `json:"last_error,omitempty"`
	Bookmark    bool         `json:"bookmark,omitempty"`
	Excerpt     string       `json:"excerpt,omitempty"`
	Site        string       `json:"site,omitempty"`
	URL         string       `json:"url,omitempty"`
	Favicon     *faviconMeta `json:"favicon,omitempty"`
	// Wall names why the last answer was not the page (login_required, paywalled, …). It is the
	// machine-readable half of LastError: the row shows the sentence, the code says what to do.
	Wall string `json:"wall,omitempty"`
	// Attached marks a snapshot the HUMAN supplied (see entities.SourceIdentity.Attached), with the
	// file it came from and its SHA-256. The refresh leaves such a source alone.
	Attached     bool   `json:"attached,omitempty"`
	AttachedFrom string `json:"attached_from,omitempty"`
	AttachedHash string `json:"attached_hash,omitempty"`
	// FromFeed marks a snapshot taken from the feed's copy of the post, with the feed it came from.
	// It is written as the string "true" by metaWithIdentity (the index reads meta fields as
	// strings), so readers must use isFromFeed, not a bool field on this struct.
	FeedHash string `json:"feed_hash,omitempty"`
	// TextChars is the rune count of a feed copy's readable body. It lets a later page attempt
	// compare the page's text with the copy: the richer text wins.
	TextChars int `json:"text_chars,omitempty"`
	// Rendered names the browser engine whose DOM the text was read from ("lightpanda"). Empty
	// means the page was read from the plain HTTP response.
	Rendered string `json:"rendered,omitempty"`
}

// faviconMeta locates a source's stored icon.
type faviconMeta struct {
	Hash string `json:"hash"`
	Ext  string `json:"ext"`
}

// defaultMinTextChars is the amount of extracted text below which a page is kept as
// a bookmark instead of being indexed.
const defaultMinTextChars = 400

// urlOwnership is who controls a dependent URL source. A wiki page and a sitemap are the two
// owners: deleting the owner removes the URLs it brought into the vault.
type urlOwnership struct {
	Origin     string
	WikiPage   string
	ParentHash string
}

// Queue records the links one owner declared and returns the ones no source knows yet.
func (uc URLs) Queue(ctx context.Context, pageID string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.WikiLinkOrigin, WikiPage: pageID}, urls)
}

// QueueSitemap claims the pages a sitemap declared. Their owner is the sitemap source itself, so
// removing the sitemap removes every page that exists only because of it.
func (uc URLs) QueueSitemap(ctx context.Context, parentHash string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.SitemapOrigin, ParentHash: parentHash}, urls)
}

// QueueFeed claims the pages a feed declared. The feed owns them the same way a sitemap does; the
// difference is the REFRESH, which keeps a page that disappears from a feed.
func (uc URLs) QueueFeed(ctx context.Context, parentHash string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.RSSOrigin, ParentHash: parentHash}, urls)
}

func (uc URLs) queue(ctx context.Context, owner urlOwnership, urls []string) ([]string, error) {
	// One list query for the whole page: the 150 links of one save must not each walk the URL table.
	// The key ignores a trailing slash, so "/article" and "/article/" are one source — the stored
	// address stays the URL as it was given first.
	known := map[string]bool{}
	if rows, err := uc.Index.ListURLSources(ctx); err == nil {
		for _, s := range rows {
			if s.URL != "" {
				known[entities.URLKey(s.URL)] = true
			}
		}
	}
	queued := make([]string, 0, len(urls))
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" || known[entities.URLKey(raw)] {
			continue
		}
		if _, err := uc.Index.GetSourceByHash(ctx, entities.URLHash(raw)); err == nil {
			continue
		}
		if err := claimLink(ctx, uc.Index, uc.Steps, uc.Clock, owner, raw); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("url: link not queued", "url", raw, "err", err)
			}
			continue
		}
		known[entities.URLKey(raw)] = true
		queued = append(queued, raw)
	}
	return queued, nil
}

// claimLink writes the row and the record of a link whose fetch is still owed: an address, a title
// (the URL itself), the owner that controls removal, and three lights that say the truth — capture
// pending, and the two steps after it skipped, because a fetch that has not happened has no text.
func claimLink(ctx context.Context, index interfaces.IndexRepository, steps interfaces.StepTracker, clock interfaces.Clock, owner urlOwnership, rawURL string) error {
	now := clock.Now()
	hash := entities.URLHash(rawURL)
	src := entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		URL: rawURL, Title: rawURL, OriginalName: rawURL,
		Origin: owner.Origin, WikiPage: owner.WikiPage, ParentHash: owner.ParentHash,
		TreeState:  entities.TreeNone,
		ImportedAt: now,
		Meta:       metaJSON(urlMeta{URL: rawURL}),
	}
	if err := index.UpsertSource(ctx, src); err != nil {
		return err
	}
	if steps == nil {
		return nil
	}
	_ = steps.SetIdentity(hash, entities.SourceIdentity{ //nolint:errcheck // the error is deliberately not actionable here
		Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
		Origin: owner.Origin, WikiPage: owner.WikiPage, ParentHash: owner.ParentHash,
	})
	_ = steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
		State: entities.StepPending, At: now, Detail: "queued: the link is fetched in the background",
	})
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
		_ = steps.Record(ctx, hash, step, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			State: entities.StepSkipped, At: now, Detail: "the link has not been fetched yet",
		})
	}
	return nil
}

// MarkFailed records that a link could not be fetched.
func (uc URLs) MarkFailed(ctx context.Context, rawURL, pageID string, cause error) (string, error) {
	return uc.MarkFailedOwned(ctx, rawURL, urlOwnership{Origin: entities.WikiLinkOrigin, WikiPage: pageID}, cause)
}

// MarkFailedSitemap records a failure for a page that a sitemap owns.
func (uc URLs) MarkFailedSitemap(ctx context.Context, rawURL, parentHash string, cause error) (string, error) {
	return uc.MarkFailedOwned(ctx, rawURL, urlOwnership{Origin: entities.SitemapOrigin, ParentHash: parentHash}, cause)
}

// MarkFailedFeed records a failure for a page that a feed owns.
func (uc URLs) MarkFailedFeed(ctx context.Context, rawURL, parentHash string, cause error) (string, error) {
	return uc.MarkFailedOwned(ctx, rawURL, urlOwnership{Origin: entities.RSSOrigin, ParentHash: parentHash}, cause)
}

// MarkFailedOwned records a failure for a URL with an explicit owner.
func (uc URLs) MarkFailedOwned(ctx context.Context, rawURL string, owner urlOwnership, cause error) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", types.New("invalid_url", "url is required", types.ErrInvalidURL)
	}
	hash := entities.URLHash(rawURL)
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		// The claim may be missing (written before a reindex, or the row was pruned): a failure
		// nobody can see is not a report, so the row is written here.
		if cErr := claimLink(ctx, uc.Index, uc.Steps, uc.Clock, owner, rawURL); cErr != nil {
			return hash, cErr
		}
		if src, err = uc.Index.GetSourceByHash(ctx, hash); err != nil {
			return hash, err
		}
	}
	now := uc.Clock.Now()
	reason := failureText(cause)
	_ = uc.Index.SetSourceURLState(ctx, hash, 0, now)                              //nolint:errcheck // the index and the step records are a light; the vault is the truth
	_ = uc.Index.SetSourceMeta(ctx, hash, metaWithError(src.Meta, rawURL, reason)) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	if uc.Steps != nil {
		_ = uc.Steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			State: entities.StepFailed, At: now, Detail: "the link could not be fetched", Error: reason,
		})
	}
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: now, Phase: "capture", Level: "error", Message: "link not fetched",
			Fields: map[string]any{"url": rawURL, "err": reason},
		})
	}
	if uc.Log != nil {
		uc.Log.Debug("link marked failed", "url", rawURL, "hash", hash)
	}
	return hash, nil
}

// failureText keeps a fetch failure readable in a row: one line, bounded, no stack.
func failureText(err error) string {
	if err == nil {
		return "the link could not be fetched"
	}
	msg := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if msg == "" {
		msg = "the link could not be fetched"
	}
	return msg
}

// metaWithError records why a fetch failed and keeps everything the source already knew.
func metaWithError(raw, url, msg string) string {
	return metaWith(raw, map[string]any{"url": url, "last_error": msg})
}

// nilIfEmpty turns an empty string into a nil map value, which metaWith reads as "remove this key".
// Provenance has two states — read from the response, read from a rendered DOM — and a written
// `"rendered": ""` would be a third one nobody means.
func nilIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// metaWith merges keys into a source's metadata WITHOUT dropping the keys it does not know. The same
// record also holds the language and the wiki page that owns the link, and a snapshot write that
// rebuilt the JSON from urlMeta used to erase both — with the origin went the answer to "may removing
// the page's link remove this source?". A nil value removes the key.
func metaWith(raw string, set map[string]any) string {
	m := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m) //nolint:errcheck // a malformed value falls back to the zero value
	}
	if m == nil {
		m = map[string]any{}
	}
	for key, value := range set {
		if value == nil {
			delete(m, key)
			continue
		}
		m[key] = value
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

// metaWithIdentity keeps the keys a SNAPSHOT does not own: the language hint, who owns the link and
// whether it is a sitemap. Empty values are left alone (an unknown language never erases a detected
// one). This is not a convenience: `origin`/`wiki_page` live ONLY in the row's meta JSON, so a write
// that rebuilt that JSON from the snapshot fields erased the ownership — and the source page then
// showed no wiki page, while removal safety had no answer. The record (<hash>.meta) is the truth;
// this copies it back onto a snapshot write.
func metaWithIdentity(raw string, src entities.Source) string {
	set := map[string]any{}
	if src.Language != "" {
		set["language"] = src.Language
	}
	if src.Origin != "" {
		set["origin"] = src.Origin
	}
	if src.WikiPage != "" {
		set["wiki_page"] = src.WikiPage
	}
	if src.ParentHash != "" {
		set["parent_hash"] = src.ParentHash
	}
	if src.Sitemap {
		// A STRING, like the repository writes it: `metaField` reads strings, and a bool here would
		// make the sitemap marker invisible to every reader.
		set["sitemap"] = "true"
	}
	if src.RSS {
		set["rss"] = "true"
	}
	if src.Listing {
		// A STRING, like sitemap and rss: metaField reads strings, and a bool would make the marker
		// invisible to every reader.
		set["listing"] = "true"
	}
	if src.FromFeed {
		set["from_feed"] = "true"
	}
	if src.FeedHash != "" {
		set["feed_hash"] = src.FeedHash
	}
	if len(set) == 0 {
		return raw
	}
	return metaWith(raw, set)
}

// URLs is the use case of the URL sources: adding a link (page, sitemap, feed or listing),
// refreshing what the cadence finds due, and recording a fetch that failed.
type URLs struct {
	Fetch   interfaces.Fetcher
	Convert interfaces.HTMLConverter
	// Render is the OPTIONAL local browser. When it is set, every page is also loaded in it and the
	// richer reading wins; nil keeps the plain HTTP response as the only source of text.
	Render       interfaces.Renderer
	Links        interfaces.LinkExtractor
	Store        interfaces.SourceStore
	Index        interfaces.IndexRepository
	Atoms        interfaces.AtomRepository
	AtomStore    interfaces.AtomStore
	Favicons     interfaces.FaviconStore
	Steps        interfaces.StepTracker
	Languages    interfaces.LanguageDetector
	SourceLog    interfaces.SourceLog
	Delete       SourceOwnedDeleter
	Reset        SourceResetter
	Clock        interfaces.Clock
	Log          *slog.Logger
	MinAge       time.Duration
	FeedMinAge   time.Duration
	MinTextChars int
}

// URLAdder adds one URL source. A handler holds this value; nil means the feature is not wired.
type URLAdder func(ctx context.Context, rawURL string, force bool) (partialentities.PartialSource, error)

// Add fetches and stores one URL that a wiki page, sitemap, feed or listing claimed. It never takes
// a source away from its owner. force=true stores a bookmark regardless of how much text the page
// has. A page a child declares is never detected as a listing: an owner is what the human added.
func (uc URLs) Add(ctx context.Context, rawURL string, force bool) (partialentities.PartialSource, error) {
	return uc.execute(ctx, rawURL, force, false, ListingNo)
}

// URLManualAdder adds a URL source the human entered by hand. A handler holds this value; nil means
// the feature is not wired.
type URLManualAdder func(ctx context.Context, rawURL string, force bool, listing ListingChoice) (partialentities.PartialSource, error)

// AddManual adds a URL the human entered. The listing choice says whether a page that declares
// articles should be offered as a listing (the default), forced into one (the confirmation), or
// never treated as one.
func (uc URLs) AddManual(ctx context.Context, rawURL string, force bool, listing ListingChoice) (partialentities.PartialSource, error) {
	return uc.execute(ctx, rawURL, force, true, listing)
}

func (uc URLs) execute(ctx context.Context, rawURL string, force, manual bool, listingChoice ListingChoice) (partialentities.PartialSource, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return partialentities.PartialSource{}, types.New("invalid_url", "url is required", types.ErrInvalidURL)
	}
	// A snapshot the HUMAN attached is not fetched again. The page is behind a wall, and whatever the
	// fetch answered could only replace the article with the gate — a login page is not a correction.
	if existing, known := uc.sourceByURL(ctx, rawURL); known && isAttached(existing.Meta) {
		if uc.Log != nil {
			uc.Log.Info("url: the snapshot was attached by hand — not fetched again",
				"url", rawURL, "hash", existing.Hash)
		}
		return partialentities.PartialFromSource(existing), nil
	}
	page, err := uc.Fetch.Fetch(ctx, rawURL)
	if err != nil {
		return partialentities.PartialSource{}, wrapFetchErr(err)
	}
	// The fetch answered, so the human's intent to own this URL can be honoured: release it from the
	// page/sitemap/feed that claimed it first. A failed fetch releases nothing.
	if manual {
		uc.releaseOwnership(ctx, rawURL)
	}
	// A wall is a RESULT, not a transport failure: the page answered, just not with itself. It is
	// recorded where the reader sees it instead of being distilled.
	if page.Wall.Blocks() {
		return uc.recordWall(ctx, rawURL, page)
	}
	if page.Status < 200 || page.Status >= 300 {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source",
			fmt.Sprintf("the URL answered HTTP %d", page.Status),
			types.ErrUnsupportedSource,
		)
	}
	// A feed is a SOURCE OF SOURCES, like a sitemap: its items become URL sources of their own. It is
	// checked before the sitemap because an Atom document is a `<feed>` and a sitemap is a `<urlset>`.
	if doc, isFeed := parseFeed(page.Body, rawURL); isFeed {
		return uc.storeFeed(ctx, rawURL, page, doc)
	}
	// A sitemap is not a page to convert: it is a page LIST. Storing it as a parent source and
	// handing the declared URLs back to the caller (which claims and fetches them) is what gives
	// every page its owner.
	if entries, isSitemap := parseSitemap(page.Body, rawURL); isSitemap {
		return uc.storeSitemap(ctx, rawURL, page, entries)
	}
	// A response that SAYS it is a feed but does not parse is reported, never converted into a page:
	// the bytes were not readable, and a bookmark would hide that.
	if looksLikeFeed(page.ContentType) {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "the URL answered with a feed that could not be read", types.ErrUnsupportedSource)
	}
	minChars := uc.MinTextChars
	if minChars <= 0 {
		minChars = defaultMinTextChars
	}
	rd, err := uc.chooseReading(ctx, rawURL, page, minChars)
	if err != nil {
		return partialentities.PartialSource{}, fmt.Errorf("convert %s: %w", rawURL, err)
	}
	// The RENDERED page may be a gate the plain response hid behind its scripts: a login form that
	// only exists after hydration. It is recorded as a wall, not as a thin page.
	if rd.Wall.Blocks() {
		blocked := page
		blocked.Status, blocked.FinalURL, blocked.Wall = rd.Status, rd.FinalURL, rd.Wall
		return uc.recordWall(ctx, rawURL, blocked)
	}
	conv := rd.Conv
	engine := rd.EngineLabel()
	title := conv.Title
	if title == "" {
		title = rawURL
	}

	body := strings.TrimSpace(conv.Markdown)
	bodyChars := utf8.RuneCountInString(body)
	bookmark := force || bodyChars < minChars
	now := uc.Clock.Now()

	// The source's bytes are the feed's copy until the page is actually read. If this add is that
	// page attempt and it is not readable, or carries less text than the copy, the copy stays — and
	// it stays on disk: the guard must run BEFORE the snapshot write, or a JavaScript shell would
	// overwrite the readable copy while the row still claimed to be one.
	existing, known := uc.sourceByURL(ctx, rawURL)
	if known && isFromFeed(existing.Meta) {
		copyChars := feedCopyTextChars(ctx, uc.Store, existing)
		reason := ""
		switch {
		case bookmark:
			reason = "the page is not readable"
		case copyChars > 0 && bodyChars < copyChars:
			reason = "the page carried less text"
		}
		if reason != "" {
			_ = uc.Index.SetSourceURLState(ctx, existing.Hash, page.Status, now)                   //nolint:errcheck // the index and the step records are a light; the vault is the truth
			_ = uc.Index.SetSourceMeta(ctx, existing.Hash, metaWith(existing.Meta, map[string]any{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
				"wall": nil, "last_error": nil,
			}))
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, existing.Hash, "info",
				"the page was tried — the feed copy is kept", map[string]any{
					"chars": bodyChars, "copy_chars": copyChars, "reason": reason,
				})
			recordStep(ctx, uc.Steps, uc.Clock, existing.Hash, entities.StepCapture, entities.StepOK,
				"feed copy · "+reason, nil)
			if uc.Log != nil {
				uc.Log.Info("url: the feed's copy is kept", "url", rawURL,
					"reason", reason, "chars", bodyChars, "copy_chars", copyChars)
			}
			stored, err := uc.Index.GetSourceByHash(ctx, existing.Hash)
			if err != nil {
				stored = existing
			}
			return partialentities.PartialFromSource(stored), nil
		}
	}

	// A listing that is already known is RE-READ as a listing, whatever the caller asked: its
	// snapshot is the article list, and writing a page over it would replace the list with a card
	// grid (or a bookmark). No pagination walk follows — that is a capture, and this is a re-read.
	// The rules read `rd.HTML`: the document the text came from, which is the rendered DOM when a
	// browser was installed and read more — a JavaScript archive's cards exist only there.
	if known && existing.Listing && uc.Links != nil && looksLikeHTML(page.ContentType) {
		if res, lErr := uc.listingResult(ctx, rd.HTML, rawURL); lErr == nil && len(res.Items) > 0 {
			return uc.storeListing(ctx, rawURL, page, res, conv.Title, existing, known)
		}
	}

	// A LISTING is a page that declares articles instead of carrying one. Detection is offered, not
	// applied: the page is stored and the answer carries how many cards were found, so the reader can
	// confirm it. A page misread as a list would never be distilled, so the default is a question,
	// never a verdict.
	var detected listing.Result
	if manual && listingChoice != ListingNo && uc.Links != nil && looksLikeHTML(page.ContentType) {
		res, detectErr := uc.listingResult(ctx, rd.HTML, rawURL)
		switch {
		case detectErr != nil:
			if uc.Log != nil {
				uc.Log.Warn("url: the page's links could not be read", "url", rawURL, "err", detectErr)
			}
		case listingChoice == ListingYes:
			if len(res.Items) == 0 {
				// The reader asked for a listing and the page has none. The numbers say WHY — "no
				// repeated block" and "a block that is not article-shaped" look the same from
				// outside, and the source log is where the reader can look it up.
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, entities.URLHash(rawURL), "warn",
					"listing: the page declares no article cards", map[string]any{
						"anchors": res.Analyzed, "group": res.GroupSize, "headings": res.Headings,
						"dates": res.Dates, "text_chars": bodyChars,
					})
				return partialentities.PartialSource{}, types.New("unsupported_source",
					"no article cards were found on this page — it is not a listing", types.ErrUnsupportedSource)
			}
			return uc.storeListing(ctx, rawURL, page, res, conv.Title, existing, known)
		case listing.Suggests(res, bodyChars, minChars, linkedInMain(res.Items, conv.Markdown)):
			// The page is stored as it is and the answer says what was found: the reader confirms
			// the listing. Nothing is reclassified here — but a card grid is not prose, and a page
			// that is about to become a list must not be distilled into claims about its own titles
			// while the reader decides. A NEW page is therefore kept as a collected link.
			detected = res
			if !known {
				bookmark = true
			}
		}
	}

	language := ""
	if uc.Languages != nil {
		language = uc.Languages.Detect(body)
		if language == "" && !bookmark {
			language = uc.Languages.Detect(title + "\n" + conv.Excerpt)
		}
	}

	favicon := fetchFavicon(ctx, uc.Fetch, uc.Favicons, uc.Log, rawURL, conv.Favicon)
	var snapshot, contentHash string
	if bookmark {
		snapshot = renderBookmarkSnapshot(rawURL, title, conv, page.Status, now, engine)
		contentHash = contentHashOf(title, snapshot)
	} else {
		snapshot = renderURLSnapshot(rawURL, title, page.Status, now, body, engine)
		contentHash = contentHashOf(title, body)
	}

	// The address of a URL source is its URL, not the hash of a snapshot of it: the snapshot is what a
	// refresh replaces, and the URL is what stays. This is what makes one URL one source.
	obj, err := uc.Store.PutAt(entities.URLHash(rawURL), ".md", []byte(snapshot))
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	// A URL that is already in the vault WITH a snapshot is REFRESHED, not filed again — one source per
	// URL, whatever happens to the page. Without this a re-added page that changed became a second
	// source beside the first (and a second derivation of it); with it, the address a URL source was
	// created with is the address every later fetch writes through.
	if known && existing.Path != "" {
		// Did this fetch actually change the text? A page that only replaces the PROVENANCE (the
		// same text as a feed copy, a cleared wall) needs no derivation rebuild; a page with new
		// bytes does.
		needsReset := contentHash != contentHashFromMeta(existing.Meta)
		if _, err := uc.Store.PutAt(existing.Hash, ".md", []byte(snapshot)); err != nil {
			return partialentities.PartialSource{}, err
		}
		existing.Ext, existing.Path, existing.Bytes = obj.Ext, obj.Path, obj.Bytes
		existing.Language = language
		// A readable page replaces the feed's copy: the marker goes with it, and so does a stale wall.
		existing.FromFeed, existing.FeedHash, existing.Wall = false, "", ""
		existing.Title, existing.Meta = title, metaWithIdentity(metaJSON(urlMeta{
			ContentHash: contentHash, Bookmark: bookmark, Excerpt: conv.Excerpt,
			Site: conv.SiteName, URL: rawURL, Favicon: favicon, Rendered: engine,
		}), existing)
		if err := uc.Index.UpsertSource(ctx, existing); err != nil {
			return partialentities.PartialSource{}, err
		}
		_ = uc.Index.SetSourceTitle(ctx, existing.Hash, title)               //nolint:errcheck // the index and the step records are a light; the vault is the truth
		_ = uc.Index.SetSourceMeta(ctx, existing.Hash, existing.Meta)        //nolint:errcheck // the index and the step records are a light; the vault is the truth
		_ = uc.Index.SetSourceURLState(ctx, existing.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		if language != "" {
			_ = uc.Index.SetSourceLanguage(ctx, existing.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if uc.Steps != nil {
				_ = uc.Steps.SetLanguage(existing.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			}
		}
		// The date the page declares for itself. It is a record, mirrored into the index; a reading
		// that found none leaves a known date alone.
		if uc.Steps != nil {
			_ = uc.Steps.SetPublished(existing.Hash, conv.PublishedAt, conv.PublishedFrom) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		}
		recordStep(ctx, uc.Steps, uc.Clock, existing.Hash, entities.StepCapture, entities.StepOK,
			fmt.Sprintf("HTTP %d · refetched", page.Status), nil)
		if uc.Steps != nil {
			_ = uc.Steps.SetIdentity(existing.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
				Kind: entities.SourceKindURL, URL: rawURL, Bookmark: bookmark, Language: language,
				OriginalName: rawURL, ContentHash: contentHash, FaviconHash: faviconHash(favicon),
				FaviconExt: faviconExt(favicon), Origin: existing.Origin, WikiPage: existing.WikiPage,
				ParentHash: existing.ParentHash, Sitemap: existing.Sitemap, RSS: existing.RSS,
				Listing: existing.Listing,
			})
		}
		stored, err := uc.Index.GetSourceByHash(ctx, existing.Hash)
		if err != nil {
			stored = existing
		}
		out := partialentities.PartialFromSource(stored)
		out.NeedsReset = needsReset
		out.ListingCandidates = len(detected.Items)
		return out, nil
	}
	src := entities.Source{
		ID: ulid.Make(), Hash: obj.Hash, Kind: entities.SourceKindURL, Ext: obj.Ext, Path: obj.Path,
		URL: rawURL, Title: title, Bytes: obj.Bytes, TreeState: entities.TreeNone, Bookmark: bookmark,
		// The raw input of a URL source IS the URL: it is what the row shows as "what this came from",
		// next to the title the document named itself.
		OriginalName: rawURL,
		Language:     language,
		ImportedAt:   now,
		Meta: metaJSON(urlMeta{
			ContentHash: contentHash, Bookmark: bookmark, Excerpt: conv.Excerpt,
			Site: conv.SiteName, URL: rawURL, Favicon: favicon, Rendered: engine,
		}),
	}
	if known {
		// A QUEUED source: a wiki page linked this URL and the fetch was still owed. The row, its
		// record and the page's reference already exist; the fetch FILLS them in place. Filing a
		// second source here would leave the page pointing at an empty one.
		src.ID, src.Hash = existing.ID, existing.Hash
		src.ImportedAt = existing.ImportedAt
		// The page that owns the link and the fact that this was collected from a page are the
		// vault's, not the fetch's: they decide whether removing the link may remove the source.
		src.Origin, src.WikiPage = existing.Origin, existing.WikiPage
		src.ParentHash, src.Sitemap = existing.ParentHash, existing.Sitemap
		if src.Hash != obj.Hash {
			moved, mErr := uc.Store.PutAt(src.Hash, ".md", []byte(snapshot))
			if mErr != nil {
				return partialentities.PartialSource{}, mErr
			}
			src.Ext, src.Path, src.Bytes = moved.Ext, moved.Path, moved.Bytes
		}
		if uc.Steps != nil {
			// The queued row carried two skipped lights ("not fetched yet"). Reset keeps the capture
			// record and clears them, so the pipeline derives the page that has just arrived.
			_ = uc.Steps.Reset(src.Hash) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		}
	}
	if known {
		// UpsertSource keeps the identity a row was created with, which is exactly right for a
		// reindex and wrong here: title, bookmark decision and snapshot metadata are the fetch's.
		if err := uc.Index.FillURLSource(ctx, src); err != nil {
			return partialentities.PartialSource{}, err
		}
	} else if err := uc.Index.UpsertSource(ctx, src); err != nil {
		return partialentities.PartialSource{}, err
	}
	if language != "" {
		_ = uc.Index.SetSourceLanguage(ctx, src.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		if uc.Steps != nil {
			_ = uc.Steps.SetLanguage(src.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		}
	}
	if err := uc.Index.SetSourceURLState(ctx, src.Hash, page.Status, now); err != nil {
		return partialentities.PartialSource{}, err
	}
	if bookmark {
		// One outline node gives the bookmark a title and excerpt on its own page; the search
		// palette finds it by its title, URL or tags. The sweep keeps ignoring it (sources.bookmark = 1).
		_ = uc.Index.ReplaceSourceNodes(ctx, src.ID, []entities.SourceNode{{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			NodeID: "0", Depth: 0, Ord: 0, Title: title, Summary: conv.Excerpt,
		}})
	}
	if uc.Log != nil {
		uc.Log.Debug("url fetched",
			"url", rawURL, "status", page.Status, "http_bytes", len(page.Body),
			"markdown_bytes", len(body), "bookmark", bookmark, "site", conv.SiteName)
	}
	stored, err := uc.Index.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		stored = src
	}
	// The detail names what was read: the site when the page declares one, "page" otherwise — a
	// leading " · HTTP 200" told the reader nothing.
	name := strings.TrimSpace(conv.SiteName)
	if name == "" {
		name = "page"
	}
	detail := fmt.Sprintf("%s · HTTP %d", name, page.Status)
	if bookmark {
		detail = fmt.Sprintf("bookmark · HTTP %d", page.Status)
	}
	if engine != "" {
		detail += " · " + engine
	}
	if uc.Steps != nil {
		_ = uc.Steps.SetPublished(src.Hash, conv.PublishedAt, conv.PublishedFrom) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	}
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK, detail, nil)
	// A URL snapshot is a .md — exactly like a Markdown source, and like a PDF's readable rendering.
	// What the source IS therefore lives in its record: without it a reindex would call this source
	// Markdown and lose the address it came from.
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			Kind: entities.SourceKindURL, URL: rawURL, Bookmark: bookmark, Language: language,
			OriginalName: rawURL, ContentHash: contentHash, FaviconHash: faviconHash(favicon),
			FaviconExt: faviconExt(favicon), Origin: src.Origin, WikiPage: src.WikiPage,
			ParentHash: src.ParentHash, Sitemap: src.Sitemap, RSS: src.RSS, Listing: src.Listing,
		})
	}
	if bookmark {
		// A bookmark is collected, not indexed: outline and distill do not apply to it, and saying
		// so is better than leaving two lights pending forever.
		recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "bookmark", nil)
		recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "bookmark", nil)
	}
	out := partialentities.PartialFromSource(stored)
	out.ListingCandidates = len(detected.Items)
	return out, nil
}

// storeSitemap writes the sitemap as a source of its own and returns the URLs it declared. The
// caller claims those URLs with this source as their owner; nothing is fetched here.
//
// A sitemap is not a bookmark: it is the parent of the pages it lists. It is not distilled either
// (its body is a URL list), so outline and distill are recorded as skipped with that reason.
func (uc URLs) storeSitemap(ctx context.Context, rawURL string, page interfaces.FetchedPage, entries []string) (partialentities.PartialSource, error) {
	now := uc.Clock.Now()
	title := sitemapTitle(rawURL)
	snapshot := renderSitemapSnapshot(rawURL, title, page.Status, now, entries)
	contentHash := contentHashOf(title, snapshot)

	obj, err := uc.Store.PutAt(entities.URLHash(rawURL), ".md", []byte(snapshot))
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	existing, known := uc.sourceByURL(ctx, rawURL)
	src := entities.Source{
		ID: ulid.Make(), Hash: obj.Hash, Kind: entities.SourceKindURL, Ext: obj.Ext, Path: obj.Path,
		URL: rawURL, Title: title, OriginalName: rawURL, Sitemap: true,
		Bytes: obj.Bytes, TreeState: entities.TreeNone, ImportedAt: now,
		Meta: metaJSON(urlMeta{ContentHash: contentHash, URL: rawURL}),
	}
	if known {
		src.ID, src.Hash = existing.ID, existing.Hash
		src.ImportedAt = existing.ImportedAt
		// Ownership outlives the snapshot: a sitemap that a wiki page or another sitemap brought in
		// keeps its owner when its own body is fetched.
		src.Origin, src.WikiPage = existing.Origin, existing.WikiPage
		src.ParentHash = existing.ParentHash
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
		fmt.Sprintf("HTTP %d · sitemap · %d URLs", page.Status, len(entries)), nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "sitemap", nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "sitemap", nil)
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
			Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash,
			Sitemap: true, ContentHash: contentHash,
		})
	}
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, src.Hash, interfaces.SourceLogEvent{
			At: now, Phase: "capture", Level: "info", Message: "sitemap expanded",
			Fields: map[string]any{"url": rawURL, "pages": len(entries)},
		})
	}
	stored, err := uc.Index.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		stored = src
	}
	res := partialentities.PartialFromSource(stored)
	res.SitemapURLs = entries
	return res, nil
}

// storeFeed writes an RSS/Atom feed as a source of its own and returns the pages its items declare.
// The caller claims those pages with this feed as their owner; nothing is fetched here.
//
// A feed is not a page and not a bookmark: it is the parent of the pages it announces. It is not
// distilled either (its body is an item list), so outline and distill are recorded as skipped.
func (uc URLs) storeFeed(ctx context.Context, rawURL string, page interfaces.FetchedPage, doc feedDocument) (partialentities.PartialSource, error) {
	now := uc.Clock.Now()
	title := feedTitle(rawURL, doc.Title)
	snapshot := renderFeedSnapshot(rawURL, title, page.Status, now, doc.Items)
	contentHash := contentHashOf(title, snapshot)

	obj, err := uc.Store.PutAt(entities.URLHash(rawURL), ".md", []byte(snapshot))
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	existing, known := uc.sourceByURL(ctx, rawURL)
	src := entities.Source{
		ID: ulid.Make(), Hash: obj.Hash, Kind: entities.SourceKindURL, Ext: obj.Ext, Path: obj.Path,
		URL: rawURL, Title: title, OriginalName: rawURL, RSS: true,
		Bytes: obj.Bytes, TreeState: entities.TreeNone, ImportedAt: now,
		Meta: metaJSON(urlMeta{ContentHash: contentHash, URL: rawURL}),
	}
	if known {
		src.ID, src.Hash = existing.ID, existing.Hash
		src.ImportedAt = existing.ImportedAt
		// Ownership outlives the snapshot: a feed that a wiki page or a sitemap brought in keeps its
		// owner when its own body is fetched.
		src.Origin, src.WikiPage = existing.Origin, existing.WikiPage
		src.ParentHash = existing.ParentHash
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
		fmt.Sprintf("HTTP %d · feed · %d items", page.Status, len(doc.Items)), nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "feed", nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "feed", nil)
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
			Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash,
			RSS: true, ContentHash: contentHash,
		})
	}
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "info", "feed expanded",
		map[string]any{"url": rawURL, "items": len(doc.Items)})
	// The items are claimed and, where the feed carries the post, filled from that copy. Only the
	// rest need a page fetch — a JavaScript page would otherwise stay a bookmark forever.
	materializer := feedMaterializer{
		Convert: uc.Convert, Store: uc.Store, Index: uc.Index, Steps: uc.Steps,
		SourceLog: uc.SourceLog, Languages: uc.Languages, Clock: uc.Clock, Log: uc.Log,
		MinChars: uc.MinTextChars,
	}
	toFetch, fromFeed := materializer.expand(ctx, src, doc.Items)
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "info", "feed items handled",
		map[string]any{"items": len(doc.Items), "from_feed": fromFeed, "to_fetch": len(toFetch)})
	stored, err := uc.Index.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		stored = src
	}
	res := partialentities.PartialFromSource(stored)
	res.RSSURLs = toFetch
	return res, nil
}

// feedLinks is a feed document's item addresses, in document order.
func feedLinks(items []feedItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Link)
	}
	return out
}

// recordWall answers a page the app was not allowed to read. Two cases, and the difference is what the
// vault already knows:
//
//   - A URL that was readable once KEEPS its snapshot. A wall is about the FETCH, not about the bytes:
//     the last text we were allowed to read is still the best answer to "what does this page say", and
//     replacing it with a login form would destroy knowledge to record a failed check.
//   - A URL with no readable snapshot gets one that says what happened, plus a red capture light and
//     outline/distill SKIPPED. That last part is the point: for a URL source the capture is the intake,
//     so without it the outline would happily build a tree out of a login page and the distill step
//     would invent claims about the gate.
func (uc URLs) recordWall(ctx context.Context, rawURL string, page interfaces.FetchedPage) (partialentities.PartialSource, error) {
	now := uc.Clock.Now()
	existing, known := uc.sourceByURL(ctx, rawURL)
	if known && existing.Path != "" && wallOf(existing.Meta) == entities.WallNone {
		_ = uc.Index.SetSourceURLState(ctx, existing.Hash, page.Status, now)                                    //nolint:errcheck // the index and the step records are a light; the vault is the truth
		_ = uc.Index.SetSourceMeta(ctx, existing.Hash, metaWithIdentity(metaWith(existing.Meta, map[string]any{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			"url": rawURL, "wall": string(page.Wall), "last_error": page.Wall.Reason(),
		}), existing))
		appendSourceLog(ctx, uc.SourceLog, uc.Clock, existing.Hash, "warn",
			"the page is walled — the stored snapshot is kept", map[string]any{
				"url": rawURL, "wall": string(page.Wall), "status": page.Status, "final_url": page.FinalURL,
			})
		stored, err := uc.Index.GetSourceByHash(ctx, existing.Hash)
		if err != nil {
			stored = existing
		}
		return partialentities.PartialFromSource(stored), nil
	}

	src := entities.Source{
		ID: ulid.Make(), Hash: entities.URLHash(rawURL), Kind: entities.SourceKindURL, Ext: ".md",
		URL: rawURL, Title: rawURL, OriginalName: rawURL, TreeState: entities.TreeNone, ImportedAt: now,
		Meta: "{}",
	}
	if known {
		src.ID, src.Hash, src.ImportedAt = existing.ID, existing.Hash, existing.ImportedAt
		src.Origin, src.WikiPage, src.ParentHash = existing.Origin, existing.WikiPage, existing.ParentHash
	}
	written, err := wallWriter{Store: uc.Store, Steps: uc.Steps, SourceLog: uc.SourceLog, Clock: uc.Clock}.
		write(ctx, src, page)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	if known {
		if err := uc.Index.FillURLSource(ctx, written); err != nil {
			return partialentities.PartialSource{}, err
		}
	} else if err := uc.Index.UpsertSource(ctx, written); err != nil {
		return partialentities.PartialSource{}, err
	}
	_ = uc.Index.SetSourceTitle(ctx, written.Hash, written.Title) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	_ = uc.Index.SetSourceMeta(ctx, written.Hash, written.Meta)   //nolint:errcheck // the index and the step records are a light; the vault is the truth
	if err := uc.Index.SetSourceURLState(ctx, written.Hash, page.Status, now); err != nil {
		return partialentities.PartialSource{}, err
	}
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(written.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
			Origin: written.Origin, WikiPage: written.WikiPage, ParentHash: written.ParentHash,
			Sitemap: written.Sitemap, RSS: written.RSS, Listing: written.Listing,
		})
	}
	stored, err := uc.Index.GetSourceByHash(ctx, written.Hash)
	if err != nil {
		stored = written
	}
	return partialentities.PartialFromSource(stored), nil
}

// wallWriter is the one place a blocked URL is written down, so the add path and the refresh path
// cannot disagree about what a wall means.
type wallWriter struct {
	Store     interfaces.SourceStore
	Steps     interfaces.StepTracker
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
}

// write stores the wall snapshot and records the three lights: capture failed with the reason, outline
// and distill skipped ("blocked"), so the pipeline never derives knowledge from a gate.
func (w wallWriter) write(ctx context.Context, src entities.Source, page interfaces.FetchedPage) (entities.Source, error) {
	now := w.Clock.Now()
	snapshot := renderWallSnapshot(src.URL, src.Title, page, now)
	obj, err := w.Store.PutAt(src.Hash, ".md", []byte(snapshot))
	if err != nil {
		return src, err
	}
	src.Ext, src.Path, src.Bytes, src.Bookmark = obj.Ext, obj.Path, obj.Bytes, false
	// The snapshot is the wall's; the OWNERSHIP is the row's. `FillURLSource`/`UpsertSource` merge it
	// into the meta they write, and the caller writes this JSON afterwards — so it has to carry the
	// keys, or a walled link from a wiki page would lose its page on the way in.
	src.Meta = metaWithIdentity(wallMeta(src.Meta, src.URL, page.Wall), src)
	if w.Steps != nil {
		_ = w.Steps.Record(ctx, src.Hash, entities.StepCapture, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			State: entities.StepFailed, At: now, Detail: page.Wall.Label(), Error: page.Wall.Reason(),
		})
		for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
			_ = w.Steps.Record(ctx, src.Hash, step, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
				State: entities.StepSkipped, At: now, Detail: "blocked: " + page.Wall.Label(),
			})
		}
	}
	appendSourceLog(ctx, w.SourceLog, w.Clock, src.Hash, "warn", "the page is walled",
		map[string]any{
			"url": src.URL, "wall": string(page.Wall), "status": page.Status, "final_url": page.FinalURL,
		})
	return src, nil
}

// wallMeta records the wall and clears a stale content hash: the bytes that hash described are gone
// (they were never read), so claiming "the page changed" against them would be a lie.
func wallMeta(raw, url string, wall entities.WallKind) string {
	return metaWith(raw, map[string]any{
		"url": url, "wall": string(wall), "last_error": wall.Reason(), "content_hash": nil,
	})
}

// renderWallSnapshot is what a reader sees for a URL that did not answer with itself: the address, what
// happened, and the way out. It is never distilled — the record says so.
func renderWallSnapshot(rawURL, title string, page interfaces.FetchedPage, at time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", at.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", page.Status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&b, "wall: %s\n", string(page.Wall))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "> Thought Keeper could not read this page: %s (HTTP %d).\n", page.Wall.Reason(), page.Status)
	fmt.Fprintf(&b, "> %s\n", page.Wall.Hint())
	b.WriteString("> The URL stays the address of this source.\n")
	return b.String()
}

// wallOf / isAttached read the two facts a URL snapshot's meta carries about its own origin. They are
// small parsers, not a second model: the JSON keys are the ones urlMeta writes.
func wallOf(meta string) entities.WallKind {
	var m urlMeta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return entities.WallNone
	}
	return entities.WallKind(m.Wall)
}

func isAttached(meta string) bool {
	var m urlMeta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return false
	}
	return m.Attached
}

// isFromFeed reports whether the snapshot came from the feed's copy of the post. The record stores
// the marker as the STRING "true" (the index reads meta fields as strings), so a bool-only parse
// would silently miss every real feed copy.
func isFromFeed(meta string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return false
	}
	switch v := m["from_feed"].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// appendSourceLog writes one line into a source's own log (best-effort: a log never fails a write).
func appendSourceLog(ctx context.Context, log interfaces.SourceLog, clock interfaces.Clock, hash, level, msg string, fields map[string]any) {
	if log == nil {
		return
	}
	log.Append(ctx, hash, interfaces.SourceLogEvent{
		At: clock.Now(), Phase: "capture", Level: level, Message: msg, Fields: fields,
	})
}

func sitemapTitle(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		path := strings.TrimSuffix(u.Path, "/")
		if path != "" && path != "/" {
			return "Sitemap · " + u.Host + path
		}
		return "Sitemap · " + u.Host
	}
	return "Sitemap"
}

// renderSitemapSnapshot is what the source page shows: the parent, its address, and the list it
// declared. The child pages are separate sources; this body is the relation in readable form.
func renderSitemapSnapshot(rawURL, title string, status int, fetchedAt time.Time, entries []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", fetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	b.WriteString("sitemap: true\n")
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "%d URL%s declared by this sitemap.\n\n", len(entries), pluralSuffix(len(entries)))
	for _, entry := range entries {
		fmt.Fprintf(&b, "- [%s](%s)\n", entry, entry)
	}
	return b.String()
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// fetchFavicon downloads and stores the page's icon (silently skipped on any
// problem: a missing icon must never fail an intake — nor a refresh).
//
// It is a FUNCTION, not a method: two paths need it (the intake and the refresh of a changed page), and
// the icon describes the site, so both must treat it the same way.
func fetchFavicon(ctx context.Context, fetch interfaces.Fetcher, icons interfaces.FaviconStore, log *slog.Logger, pageURL, declared string) *faviconMeta {
	if icons == nil {
		return nil
	}
	iconURL := resolveIconURL(pageURL, declared)
	if iconURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	page, err := fetch.Fetch(ctx, iconURL)
	if err != nil || page.Status < 200 || page.Status >= 300 {
		return nil
	}
	if len(page.Body) == 0 || len(page.Body) > 256<<10 {
		return nil
	}
	if !strings.HasPrefix(page.ContentType, "image/") && !strings.Contains(page.ContentType, "icon") &&
		!strings.Contains(strings.ToLower(page.ContentType), "octet-stream") {
		return nil
	}
	fav, err := icons.Put(ctx, page.ContentType, bytes.NewReader(page.Body))
	if err != nil {
		return nil
	}
	if log != nil {
		log.Debug("favicon stored", "url", pageURL, "icon", iconURL, "hash", fav.Hash)
	}
	return &faviconMeta{Hash: fav.Hash, Ext: fav.Ext}
}

// resolveIconURL turns the declared icon (may be relative, may be empty) into an
// absolute URL, falling back to the site's /favicon.ico.
func resolveIconURL(pageURL, declared string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	if declared != "" {
		if ref, err := url.Parse(declared); err == nil {
			return base.ResolveReference(ref).String()
		}
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/favicon.ico"}).String()
}

// URLRefresher refreshes every URL source the cadence finds due. A handler holds this value; nil
// means the feature is not wired.
type URLRefresher func(ctx context.Context) (RefreshResult, error)

// URLRefresherAll refreshes every URL source, due or not.
type URLRefresherAll func(ctx context.Context) (RefreshResult, error)

// URLRefresherOne refreshes one URL source.
type URLRefresherOne func(ctx context.Context, hash string) (RefreshResult, error)

// URLRefresherOneForce refreshes one URL source even if it is not due.
type URLRefresherOneForce func(ctx context.Context, hash string) (RefreshResult, error)

// DiscoveredChildren are the NEW pages an owner declared in this pass (already claimed as sources).
// The caller fetches them in the background; `Origin` says who owns them.
type DiscoveredChildren struct {
	ParentHash string
	Origin     string
	URLs       []string
}

// RefreshResult summarizes one pass.
type RefreshResult struct {
	Checked  int
	Changed  int
	Failed   int
	Blocked  int
	Attached int
	// Replaced counts attached snapshots a forced fetch replaced with a regular one. A wall or a
	// failure leaves the attachment in place and does not count here.
	Replaced int
	// Discovered are the new children sitemaps and feeds declared.
	Discovered []DiscoveredChildren
	// Removed are the pages a SITEMAP no longer lists. A feed never removes an item.
	Removed int
	Sources []partialentities.PartialSource
	// ChangedURLs are the pages that actually changed, so the caller knows whether the association
	// layer has something to regroup.
	ChangedURLs []string
	// Resets are the sources whose snapshot was replaced by DIFFERENT text. The refresh runs under
	// the shared gate, so it must not throw a derivation away itself: a document run may be in
	// flight. The caller schedules the app's exclusive reset for each entry and derives again.
	Resets []SnapshotReplacement
	// LastError is why the last source that failed did so. The counts are enough for a pass; the
	// per-source Refresh button has one row to explain, and "1 failed" alone is not an explanation.
	LastError string
}

// SnapshotReplacement names one source whose bytes changed and the reason a reader should see in
// its log after the old derivation was rebuilt.
type SnapshotReplacement struct {
	Hash   string
	Reason string
}

// Refresh refreshes the URL sources that are DUE (a scheduled pass: the per-kind intervals decide).
func (uc URLs) Refresh(ctx context.Context) (RefreshResult, error) {
	return uc.refreshPass(ctx, true)
}

// RefreshAll refreshes every URL source.
func (uc URLs) RefreshAll(ctx context.Context) (RefreshResult, error) {
	return uc.refreshPass(ctx, false)
}

// RefreshOne refreshes one URL source.
func (uc URLs) RefreshOne(ctx context.Context, hash string) (RefreshResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return RefreshResult{}, err
	}
	if src.URL == "" {
		return RefreshResult{}, types.New("unsupported_source",
			"this source has no URL to refresh", types.ErrUnsupportedSource)
	}
	return uc.executeRows(ctx, []entities.Source{src}, false, false)
}

// RefreshOneForce refreshes one URL source even when it is not due.
func (uc URLs) RefreshOneForce(ctx context.Context, hash string) (RefreshResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return RefreshResult{}, err
	}
	if src.Kind != entities.SourceKindURL || src.URL == "" {
		return RefreshResult{}, types.New("unsupported_source",
			"only a URL source can be fetched again", types.ErrUnsupportedSource)
	}
	if src.Sitemap || src.RSS || src.Listing {
		return RefreshResult{}, types.New("unsupported_source",
			"a sitemap, a feed or a listing is a list, not a page to attach", types.ErrUnsupportedSource)
	}
	if !isAttached(src.Meta) && !src.Bookmark {
		return RefreshResult{}, types.New("unsupported_source",
			"this snapshot is not attached and not a collected link; refresh the URL instead",
			types.ErrUnsupportedSource)
	}
	return uc.executeRows(ctx, []entities.Source{src}, false, true)
}

func (uc URLs) refreshPass(ctx context.Context, scheduled bool) (RefreshResult, error) {
	rows, err := uc.Index.ListURLSources(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	return uc.executeRows(ctx, rows, scheduled, false)
}

func (uc URLs) executeRows(ctx context.Context, rows []entities.Source, scheduled, ignoreAttached bool) (RefreshResult, error) {
	var res RefreshResult
	seen := make(map[string]bool)
	for _, s := range rows {
		if s.URL == "" || seen[entities.URLKey(s.URL)] {
			continue
		}
		seen[entities.URLKey(s.URL)] = true
		// The meta is the truth about where the bytes came from and why the last fetch was refused.
		// Capture both BEFORE a successful page write clears them.
		fromFeed := isFromFeed(s.Meta)
		wasWalled := wallOf(s.Meta) != entities.WallNone
		// A snapshot the human attached is not checked at all. It exists BECAUSE the page cannot be
		// fetched; asking the site every week would only write a 403 onto an article that is fine.
		// A FORCED fetch (the explicit "try the URL again") asks anyway, and keeps the attachment if
		// the answer is still a wall.
		attached := isAttached(s.Meta)
		if attached && !ignoreAttached {
			res.Attached++
			continue
		}
		now := uc.Clock.Now()
		if scheduled {
			// A feed (its item list) is checked daily; a page and a sitemap weekly. A kind whose
			// interval is zero is not refreshed automatically at all.
			age := uc.MinAge
			if s.RSS || s.Listing {
				// A feed announces new posts and a listing shows them: both change far more often
				// than a page's text does.
				age = uc.FeedMinAge
			}
			if age <= 0 {
				continue
			}
			// Due means "not checked within the cadence". A source never checked is always due.
			if !s.URLChecked.IsZero() && now.Sub(s.URLChecked) < age {
				continue
			}
		}
		res.Checked++
		page, fetchErr := uc.Fetch.Fetch(ctx, s.URL)
		if fetchErr != nil {
			res.Failed++
			res.LastError = fetchErr.Error()
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, 0, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if attached {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the URL was tried again — the fetch failed; the attached snapshot was kept",
					map[string]any{"error": fetchErr.Error()})
			}
			if fromFeed {
				// The feed's copy is still the readable form of this post. A failed page attempt is
				// not a failed source: the copy stays, the attempt is written down.
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the page could not be fetched — the feed copy is kept",
					map[string]any{"error": fetchErr.Error()})
				recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
					"feed copy · page unreachable", nil)
			}
			if uc.Log != nil {
				uc.Log.Warn("url refresh failed", "url", s.URL, "err", fetchErr)
			}
			continue
		}
		// A wall is not a changed page: the site did not let us see one. A source that has bytes keeps
		// them (the last readable text stays the truth about what the page said); a queued row that
		// never got any gets the snapshot that says why.
		if page.Wall.Blocks() {
			res.Blocked++
			res.LastError = page.Wall.Reason()
			if s.Path == "" {
				if written, wErr := (wallWriter{
					Store: uc.Store, Steps: uc.Steps, SourceLog: uc.SourceLog, Clock: uc.Clock,
				}).write(ctx, s, page); wErr == nil {
					written.Origin, written.WikiPage, written.ParentHash = s.Origin, s.WikiPage, s.ParentHash
					if err := uc.Index.FillURLSource(ctx, written); err != nil {
						res.Failed++
						res.LastError = err.Error()
						continue
					}
					_ = uc.Index.SetSourceTitle(ctx, s.Hash, written.Title) //nolint:errcheck // the index and the step records are a light; the vault is the truth
					_ = uc.Index.SetSourceMeta(ctx, s.Hash, written.Meta)   //nolint:errcheck // the index and the step records are a light; the vault is the truth
					if uc.Steps != nil {
						_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
							Kind: entities.SourceKindURL, URL: s.URL, OriginalName: s.URL,
							Origin: s.Origin, WikiPage: s.WikiPage, ParentHash: s.ParentHash,
						})
					}
				}
			} else {
				_ = uc.Index.SetSourceMeta(ctx, s.Hash, metaWithIdentity(metaWith(s.Meta, map[string]any{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
					"url": s.URL, "wall": string(page.Wall), "last_error": page.Wall.Reason(),
				}), s))
			}
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if attached {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the URL was tried again — still blocked; the attached snapshot was kept",
					map[string]any{"wall": string(page.Wall), "http_status": page.Status})
			}
			if fromFeed && s.Path != "" {
				// The wall describes the fetch, not the bytes. The copy remains readable and the
				// capture light stays green; the attempt and its reason are visible in the log.
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the page is still walled — the feed copy is kept",
					map[string]any{"wall": string(page.Wall), "http_status": page.Status})
				recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
					"feed copy · page walled", nil)
			} else if s.Path != "" && !attached {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the page is still walled — the stored snapshot is kept",
					map[string]any{"wall": string(page.Wall), "http_status": page.Status})
			}
			if uc.Log != nil {
				uc.Log.Info("url refresh: blocked", "url", s.URL, "wall", string(page.Wall), "status", page.Status)
			}
			continue
		}
		if page.Status < 200 || page.Status >= 300 {
			res.Failed++
			res.LastError = fmt.Sprintf("HTTP %d", page.Status)
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if attached {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					fmt.Sprintf("the URL was tried again — HTTP %d; the attached snapshot was kept", page.Status),
					nil)
			}
			if fromFeed {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					fmt.Sprintf("the page answered HTTP %d — the feed copy is kept", page.Status),
					map[string]any{"http_status": page.Status})
				recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
					fmt.Sprintf("feed copy · HTTP %d", page.Status), nil)
			}
			if uc.Log != nil {
				uc.Log.Info("url refresh: not ok", "url", s.URL, "status", page.Status)
			}
			continue
		}
		// A sitemap's, feed's or listing's snapshot is a LIST, not HTML: re-expanding it here is what
		// brings new pages in (and, for a sitemap, what removes the ones it no longer lists). Its
		// children are fetched as their own sources.
		if s.Sitemap || s.RSS || s.Listing {
			parent, queued, removed, parentErr := uc.refreshParent(ctx, s, page, now)
			if parentErr != nil {
				res.Failed++
				res.LastError = parentErr.Error()
				_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
				if uc.Log != nil {
					uc.Log.Warn("url refresh: parent not expanded", "url", s.URL, "err", parentErr)
				}
				continue
			}
			res.Removed += removed
			if len(queued) > 0 {
				res.Discovered = append(res.Discovered, DiscoveredChildren{
					ParentHash: s.Hash, Origin: s.ChildOrigin(), URLs: queued,
				})
			}
			res.Sources = append(res.Sources, parent)
			if uc.Log != nil {
				uc.Log.Info("url refresh: parent expanded", "url", s.URL,
					"new", len(queued), "removed", removed)
			}
			continue
		}

		minChars := uc.MinTextChars
		if minChars <= 0 {
			minChars = defaultMinTextChars
		}
		rd, convErr := uc.chooseReading(ctx, s.URL, page, minChars)
		if convErr != nil {
			res.Failed++
			res.LastError = convErr.Error()
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if attached {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the URL was tried again — the page could not be converted; the attached snapshot was kept",
					map[string]any{"error": convErr.Error()})
			}
			if fromFeed {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the page could not be converted — the feed copy is kept",
					map[string]any{"error": convErr.Error()})
				recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
					"feed copy · page unreadable", nil)
			}
			continue
		}
		// The rendered page may be a gate the plain response hid behind its scripts.
		if rd.Wall.Blocks() {
			blocked := page
			blocked.Status, blocked.FinalURL, blocked.Wall = rd.Status, rd.FinalURL, rd.Wall
			if partial, wallErr := uc.recordWall(ctx, s.URL, blocked); wallErr == nil {
				res.Blocked++
				res.Sources = append(res.Sources, partial)
			}
			continue
		}
		conv := rd.Conv
		engine := rd.EngineLabel()
		body := strings.TrimSpace(conv.Markdown)
		bodyChars := utf8.RuneCountInString(body)
		title := conv.Title
		if title == "" {
			title = s.URL
		}
		// A retried attachment is only replaced by a page that is actually readable. A JavaScript
		// shell or a teaser that carries a little navigation text must not overwrite the human's copy.
		if attached && ignoreAttached {
			if bodyChars < minChars {
				res.Failed++
				res.LastError = "the page gave no readable text"
				_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the URL was tried again — no readable text; the attached snapshot was kept",
					map[string]any{"chars": bodyChars, "min": minChars})
				continue
			}
		}

		// A page that answers with too little text (a JavaScript shell, a stub) must not overwrite a
		// snapshot that is readable. A feed copy has its own rule below (it compares lengths); a
		// queued row has no snapshot to keep.
		if s.Path != "" && !attached && !fromFeed && bodyChars < minChars {
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
				"the page gave no readable text — the stored snapshot was kept",
				map[string]any{"chars": bodyChars, "min": minChars, "http_status": page.Status})
			if !wasWalled {
				recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
					"stored snapshot · page not readable", nil)
			}
			if uc.Log != nil {
				uc.Log.Info("url refresh: no readable text — the stored snapshot is kept",
					"url", s.URL, "chars", bodyChars, "min", minChars)
			}
			continue
		}

		// A copy taken from a FEED is a provisional snapshot: the page is always tried, and the
		// richer text wins. A page that is not readable, or carries less text than the copy, leaves
		// the copy in place — the same rule the weekly pass applies.
		copyChars := 0
		if fromFeed {
			copyChars = feedCopyTextChars(ctx, uc.Store, s)
			reason := ""
			switch {
			case bodyChars < minChars:
				reason = "the page is not readable"
			case copyChars > 0 && bodyChars < copyChars:
				reason = "the page carried less text"
			}
			if reason != "" {
				_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)            //nolint:errcheck // the index and the step records are a light; the vault is the truth
				_ = uc.Index.SetSourceMeta(ctx, s.Hash, metaWith(s.Meta, map[string]any{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
					"wall": nil, "last_error": nil,
				}))
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info",
					"the page was tried — the feed copy is kept", map[string]any{
						"chars": bodyChars, "copy_chars": copyChars, "min": minChars, "reason": reason,
					})
				recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
					"feed copy · "+reason, nil)
				if uc.Log != nil {
					uc.Log.Info("url refresh: the feed's copy is kept", "url", s.URL,
						"reason", reason, "chars", bodyChars, "copy_chars", copyChars)
				}
				continue
			}
		}

		// A bookmark stays a bookmark: only its status is refreshed (the explicit "try the page
		// again" is the way to re-read one — ignoreAttached is that call).
		if s.Bookmark && !ignoreAttached {
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if uc.Log != nil {
				uc.Log.Debug("bookmark refreshed", "url", s.URL, "status", page.Status)
			}
			continue
		}
		if body == "" {
			res.Failed++
			res.LastError = "the page gave no readable text"
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if attached {
				appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "warn",
					"the URL was tried again — no readable text; the attached snapshot was kept", nil)
			}
			continue
		}

		language := ""
		if uc.Languages != nil {
			language = uc.Languages.Detect(body)
		}
		if language != "" {
			s.Language = language
			_ = uc.Index.SetSourceLanguage(ctx, s.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if uc.Steps != nil {
				_ = uc.Steps.SetLanguage(s.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			}
		}

		contentHash := contentHashOf(title, body)
		// What the SOURCE said before this fetch. The favicon repair below writes the new hash into
		// s.Meta; comparing after it would call every changed page "unchanged" on a source whose
		// icon was missing (LWN was exactly that case).
		previousContentHash := contentHashFromMeta(s.Meta)
		// A URL record written before the identity carried the icon has no favicon reference. A
		// refresh is the natural repair: fetch it, write it into the index AND the source record,
		// then the next reindex cannot lose it again.
		favicon := faviconFromMeta(s.Meta)
		if favicon == nil {
			favicon = fetchFavicon(ctx, uc.Fetch, uc.Favicons, uc.Log, s.URL, conv.Favicon)
			if favicon != nil {
				s.Meta = metaWithFavicon(s.Meta, s.URL, contentHash, favicon)
				_ = uc.Index.SetSourceMeta(ctx, s.Hash, s.Meta) //nolint:errcheck // the index and the step records are a light; the vault is the truth
				if language != "" {
					_ = uc.Index.SetSourceLanguage(ctx, s.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
				}
				if uc.Steps != nil {
					_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
						Kind: entities.SourceKindURL, URL: s.URL, Bookmark: s.Bookmark, Language: language,
						OriginalName: s.OriginalName, ContentHash: contentHash,
						FaviconHash: faviconHash(favicon), FaviconExt: faviconExt(favicon),
						FromFeed: s.FromFeed, FeedHash: s.FeedHash,
					})
				}
			}
		}
		sameContent := contentHash == previousContentHash
		if sameContent && !attached && !fromFeed && !wasWalled {
			// Same content: just record that we checked and the page still answers. A feed copy or a
			// source whose last fetch was walled falls through: the provenance must change even when
			// the text did not (the page WAS read).
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if uc.Log != nil {
				uc.Log.Debug("url unchanged", "url", s.URL, "status", page.Status)
			}
			continue
		}

		snapshot := renderURLSnapshot(s.URL, title, page.Status, now, body, engine)
		// The snapshot is refreshed IN PLACE, at the address the source already has. A changed page is
		// not a new source: filing one made a duplicate row — and a second, from-scratch derivation —
		// for every page that changed, so a page with hundreds of parts paid for it again each time.
		// The source's identity is the address it was created with; the snapshot behind it is what a
		// refresh replaces.
		obj, err := uc.Store.PutAt(s.Hash, s.Ext, []byte(snapshot))
		if err != nil {
			res.Failed++
			res.LastError = err.Error()
			continue
		}
		// The icon belongs to the SITE, not to one snapshot: the fetch above already repaired a
		// missing icon; if it was still unavailable, keep the reference we had rather than writing a
		// meta without one.
		meta := metaWithFavicon(s.Meta, s.URL, contentHash, favicon)
		// A readable page replaces the feed's copy: the snapshot is now the page's, so the marker
		// goes. The wall describes the LAST fetch, not the bytes: a successful read clears it too.
		meta = metaWith(meta, map[string]any{
			"from_feed": nil, "feed_hash": nil, "wall": nil, "last_error": nil, "text_chars": nil,
			// Which engine produced the text. A plain read clears it, so the record never keeps a
			// render that a later fetch replaced.
			"rendered": nilIfEmpty(engine),
		})
		if attached {
			// The fetched page is the source now, not the human's workaround. Clearing the marker is
			// what lets the weekly refresh treat it as an ordinary URL again.
			meta = metaWith(meta, map[string]any{
				"attached": nil, "attached_from": nil, "attached_hash": nil,
			})
		}
		// A COLLECTED LINK that the render finally made readable stops being a bookmark: the page is
		// indexed from now on, and the record says why it changed. This is the explicit "try the page
		// again" only — the weekly pass leaves a bookmark alone.
		becamePage := s.Bookmark && ignoreAttached
		if becamePage {
			meta = metaWith(meta, map[string]any{"bookmark": nil})
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info",
				"the page is readable — the collected link became a page", map[string]any{
					"chars": bodyChars, "http_status": page.Status, "rendered": nilIfEmpty(engine),
				})
		}
		updated := s
		updated.Ext, updated.Path, updated.Bytes = obj.Ext, obj.Path, obj.Bytes
		updated.Title, updated.Meta = title, meta
		updated.Language = language
		updated.Bookmark = false
		updated.Attached, updated.AttachedFrom = false, ""
		// The page is the source now: neither the feed marker nor the wall survives a successful read.
		updated.FromFeed, updated.FeedHash, updated.Wall = false, "", ""
		if err := uc.Index.UpsertSource(ctx, updated); err != nil {
			res.Failed++
			res.LastError = err.Error()
			continue
		}
		// The row's identity and its metadata are the vault's, and the index mirrors them: the upsert
		// refreshes only the derived columns, so both are written explicitly.
		if err := uc.Index.SetSourceTitle(ctx, s.Hash, title); err != nil && uc.Log != nil {
			uc.Log.Warn("url refresh: title", "url", s.URL, "err", err)
		}
		if err := uc.Index.SetSourceMeta(ctx, s.Hash, meta); err != nil && uc.Log != nil {
			uc.Log.Warn("url refresh: meta", "url", s.URL, "err", err)
		}
		if becamePage {
			if err := uc.Index.SetSourceBookmark(ctx, s.Hash, false); err != nil && uc.Log != nil {
				uc.Log.Warn("url refresh: bookmark", "url", s.URL, "err", err)
			}
		}
		if language != "" {
			_ = uc.Index.SetSourceLanguage(ctx, s.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		}
		if err := uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("url state", "url", s.URL, "err", err)
			}
		}
		// The snapshot is a captured source of its own: the bytes are fetched and readable. A row that
		// had NO object until now was a queued (or failed) link: its skipped outline and distill lights
		// have to be re-armed, or the page that finally arrived would never be derived.
		if s.Path == "" && uc.Steps != nil {
			_ = uc.Steps.Reset(s.Hash) //nolint:errcheck // the index and the step records are a light; the vault is the truth
		}
		captureDetail := fmt.Sprintf("HTTP %d · refetched", page.Status)
		if engine != "" {
			captureDetail += " · " + engine
		}
		recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK, captureDetail, nil)
		if uc.Steps != nil {
			_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
				Kind: entities.SourceKindURL, URL: s.URL, Language: language, OriginalName: s.URL,
				ContentHash: contentHash, FaviconHash: faviconHash(favicon), FaviconExt: faviconExt(favicon),
			})
		}
		if attached {
			// The old derivation was built on OTHER bytes. A same-content fetch leaves it alone; a
			// changed one is reset, so the new snapshot is not mixed with claims about the old one.
			reset := false
			if !sameContent && uc.Reset != nil {
				reset = true
				if _, rErr := uc.Reset(ctx, s.Hash); rErr != nil {
					reset = false
					if uc.Log != nil {
						uc.Log.Warn("url refetch: attached snapshot replaced, derivation not reset",
							"url", s.URL, "hash", s.Hash, "err", rErr)
					}
				}
			}
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info",
				"attached snapshot replaced by a fetch", map[string]any{
					"file": s.AttachedFrom, "http_status": page.Status, "reset": reset,
				})
			res.Replaced++
		}
		// The source's bytes changed. The old derivation belongs to other text, and a document run
		// may be in flight: the caller must rebuild it through the app's exclusive reset. A queued
		// row (Path == "") needs no app reset — it only re-arms its skipped steps below.
		if !sameContent && s.Path != "" && !attached {
			reason := "the page changed — the derivation was rebuilt"
			switch {
			case becamePage:
				reason = "the collected link became a page — the derivation was rebuilt"
			case fromFeed:
				reason = "the page replaced the feed copy — the derivation was rebuilt"
			case wasWalled:
				reason = "the wall cleared and the page was read — the derivation was rebuilt"
			}
			res.Resets = append(res.Resets, SnapshotReplacement{Hash: s.Hash, Reason: reason})
		}
		// The provenance change is written down even when the text is identical: a feed copy that was
		// finally read, and a wall that cleared, are facts about the source, not about its length.
		switch {
		case fromFeed && sameContent:
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info",
				"the page was read — the same text as the feed copy; the feed marker was cleared",
				map[string]any{"chars": bodyChars, "http_status": page.Status})
		case fromFeed:
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info",
				"the page replaced the feed copy", map[string]any{
					"chars": bodyChars, "copy_chars": copyChars, "http_status": page.Status,
				})
		case wasWalled:
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info",
				"the page is readable again — the wall cleared",
				map[string]any{"chars": bodyChars, "http_status": page.Status})
		}
		if !sameContent {
			res.Changed++
			res.ChangedURLs = append(res.ChangedURLs, s.URL)
		}
		res.Sources = append(res.Sources, partialentities.PartialFromSource(updated))
		if uc.Log != nil {
			uc.Log.Info("url changed", "url", s.URL, "hash", s.Hash,
				"markdown_bytes", len(body), "replaced_attachment", attached)
		}
	}
	return res, nil
}

// refreshParent re-expands a sitemap or a feed and keeps its children in step with it. The difference
// is what the list PROMISES: a sitemap says "these are the pages", so a page that left it is deleted
// (a 1:1 reconciliation); a feed says "these are the newest items", so a page that left it is kept —
// the archive of what the feed announced is the point. Only deleting the owner removes them.
func (uc URLs) refreshParent(ctx context.Context, s entities.Source, page interfaces.FetchedPage, now time.Time) (partialentities.PartialSource, []string, int, error) {
	queue := uc
	var (
		title       string
		snapshot    string
		contentHash string
		detail      string
		queued      []string
		removed     int
		err         error
	)
	if s.Sitemap {
		entries, ok := parseSitemap(page.Body, s.URL)
		if !ok {
			return partialentities.PartialSource{}, nil, 0, errors.New("the sitemap could not be read")
		}
		title = sitemapTitle(s.URL)
		snapshot = renderSitemapSnapshot(s.URL, title, page.Status, now, entries)
		contentHash = contentHashOf(title, snapshot)
		detail = fmt.Sprintf("HTTP %d · sitemap · %d URLs", page.Status, len(entries))
		if queued, err = queue.QueueSitemap(ctx, s.Hash, entries); err != nil {
			return partialentities.PartialSource{}, nil, 0, err
		}
		if removed, err = uc.removeGoneChildren(ctx, s, entries); err != nil {
			return partialentities.PartialSource{}, nil, 0, err
		}
	} else if s.Listing {
		// A listing is refreshed through its FIRST page only. A blog index and a newsletter archive
		// are newest-first: a new article is on page 1, and walking the pagination again every day
		// would fetch the archive to learn nothing.
		res, rErr := uc.listingResult(ctx, page.Body, s.URL)
		if rErr != nil {
			return partialentities.PartialSource{}, nil, 0, rErr
		}
		// A JavaScript archive sends a shell: its cards exist only after the scripts ran, so a
		// refresh of one has to render — the daily check must see the newest article.
		if len(res.Items) == 0 {
			if rendered, ok := uc.renderedListing(ctx, s.URL, res); ok {
				res = rendered
			}
		}
		if len(res.Items) == 0 {
			return partialentities.PartialSource{}, nil, 0, errors.New("the listing could not be read")
		}
		// Only page 1 was fetched. The archive the initial walk collected is kept: a listing is a
		// window, and a snapshot that forgot every article older than the newest page would read as
		// data loss.
		fresh := len(res.Items)
		items := mergeListingItems(res.Items, uc.listingSnapshotItems(s))
		title = s.Title
		if title == "" {
			title = listingTitle(s.URL)
		}
		snapshot = renderListingSnapshot(s.URL, title, page.Status, now, items)
		contentHash = contentHashOf(title, snapshot)
		detail = fmt.Sprintf("HTTP %d · listing · %d articles · %d on page 1", page.Status, len(items), fresh)
		if queued, err = queue.QueueListing(ctx, s.Hash, itemURLs(items)); err != nil {
			return partialentities.PartialSource{}, nil, 0, err
		}
	} else {
		doc, ok := parseFeed(page.Body, s.URL)
		if !ok {
			return partialentities.PartialSource{}, nil, 0, errors.New("the feed could not be read")
		}
		title = feedTitle(s.URL, doc.Title)
		snapshot = renderFeedSnapshot(s.URL, title, page.Status, now, doc.Items)
		contentHash = contentHashOf(title, snapshot)
		materializer := feedMaterializer{
			Convert: uc.Convert, Store: uc.Store, Index: uc.Index, Steps: uc.Steps,
			SourceLog: uc.SourceLog, Languages: uc.Languages, Clock: uc.Clock, Log: uc.Log,
			MinChars: uc.MinTextChars,
		}
		var fromFeed int
		queued, fromFeed = materializer.expand(ctx, s, doc.Items)
		detail = fmt.Sprintf("HTTP %d · feed · %d items · %d feed copies · %d pages queued",
			page.Status, len(doc.Items), fromFeed, len(queued))
	}

	obj, err := uc.Store.PutAt(s.Hash, s.Ext, []byte(snapshot))
	if err != nil {
		return partialentities.PartialSource{}, nil, 0, err
	}
	updated := s
	updated.Ext, updated.Path, updated.Bytes = obj.Ext, obj.Path, obj.Bytes
	updated.Title = title
	updated.Meta = metaWithIdentity(metaWith(s.Meta, map[string]any{
		"content_hash": contentHash, "url": s.URL, "last_error": nil, "wall": nil,
	}), s)
	if err := uc.Index.FillURLSource(ctx, updated); err != nil {
		return partialentities.PartialSource{}, nil, 0, err
	}
	_ = uc.Index.SetSourceTitle(ctx, s.Hash, title)               //nolint:errcheck // the index and the step records are a light; the vault is the truth
	_ = uc.Index.SetSourceMeta(ctx, s.Hash, updated.Meta)         //nolint:errcheck // the index and the step records are a light; the vault is the truth
	_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now) //nolint:errcheck // the index and the step records are a light; the vault is the truth
	if uc.Steps != nil {
		icon := faviconFromMeta(updated.Meta)
		_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
			Kind: entities.SourceKindURL, URL: s.URL, Language: s.Language, OriginalName: s.URL,
			ContentHash: contentHash, FaviconHash: faviconHash(icon), FaviconExt: faviconExt(icon),
			Origin: s.Origin, WikiPage: s.WikiPage, ParentHash: s.ParentHash,
			Sitemap: s.Sitemap, RSS: s.RSS, Listing: s.Listing,
		})
	}
	recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK, detail, nil)
	// A feed or a sitemap is a LIST: the refresh just fetched it, and the document steps still do not
	// apply. Re-recording them keeps the .meta the truth even when an older record lost them.
	parentDetail := "feed"
	switch {
	case s.Sitemap:
		parentDetail = "sitemap"
	case s.Listing:
		parentDetail = "listing"
	}
	recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepOutline, entities.StepSkipped, parentDetail, nil)
	recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepDistill, entities.StepSkipped, parentDetail, nil)
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "info", "expanded again", map[string]any{
		"url": s.URL, "new": len(queued), "removed": removed, "title": title,
	})
	stored, err := uc.Index.GetSourceByHash(ctx, s.Hash)
	if err != nil {
		stored = updated
	}
	return partialentities.PartialFromSource(stored), queued, removed, nil
}

// removeGoneChildren deletes the pages a SITEMAP no longer lists. A feed never gets here: its items
// are a window, not an inventory. A child the human released is no longer a child (the release clears
// ParentHash), so a manual addition always survives.
func (uc URLs) removeGoneChildren(ctx context.Context, parent entities.Source, entries []string) (int, error) {
	if uc.Delete == nil {
		return 0, nil
	}
	keep := make(map[string]bool, len(entries))
	for _, entry := range entries {
		keep[entities.URLKey(entry)] = true
	}
	children, err := uc.Index.ListSourceChildren(ctx, parent.Hash, "")
	if err != nil {
		return 0, err
	}
	hashes := make([]string, 0, len(children))
	for _, child := range children {
		if child.ParentHash != parent.Hash || child.Origin != entities.SitemapOrigin {
			continue
		}
		if child.URL != "" && keep[entities.URLKey(child.URL)] {
			continue
		}
		hashes = append(hashes, child.Hash)
	}
	if len(hashes) == 0 {
		return 0, nil
	}
	// ONE delete scope: a page that left the sitemap but is still declared by a listing or a wiki
	// page is re-homed, not removed.
	result, err := uc.Delete(ctx, hashes...)
	if err != nil && uc.Log != nil {
		uc.Log.Warn("sitemap refresh: pages not removed", "sitemap", parent.Hash, "err", err)
	}
	return len(result.Removed), nil
}

// metaWithFavicon preserves the other URL metadata (bookmark, excerpt, site) while setting the
// content hash and icon. Writing a fresh urlMeta here is how the old code lost fields. The last
// failure is cleared: a snapshot that has just arrived is the proof that the link answers.
func metaWithFavicon(raw, url, contentHash string, favicon *faviconMeta) string {
	set := map[string]any{"url": url, "last_error": nil, "wall": nil, "favicon": nil}
	if contentHash != "" {
		set["content_hash"] = contentHash
	}
	if favicon != nil {
		set["favicon"] = favicon
	}
	return metaWith(raw, set)
}

func contentHashOf(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\n\n" + body))
	return hex.EncodeToString(sum[:])
}

// wrapFetchErr keeps an already-coded error (e.g. the SSRF guard's "forbidden")
// and otherwise reports a transport failure as "fetch_failed".
func wrapFetchErr(err error) error {
	if _, ok := errors.AsType[*types.Error](err); ok {
		return err
	}
	return types.New("fetch_failed", err.Error(), err)
}

// faviconFromMeta is the icon a source already had: a refresh carries it over rather than losing it,
// because the icon describes the SITE and a new snapshot is still the same site.
func faviconFromMeta(meta string) *faviconMeta {
	var m urlMeta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return nil
	}
	return m.Favicon
}

// FindURLSource finds the source a URL already has, if any. Comparison ignores a trailing slash and
// the fragment (see entities.URLKey): "/article" and "/article/" address one page, so a second intake
// must fill the source that is already there instead of filing a second one.
func FindURLSource(ctx context.Context, index interfaces.IndexRepository, rawURL string) (entities.Source, bool) {
	rows, err := index.ListURLSources(ctx)
	if err != nil {
		return entities.Source{}, false
	}
	key := entities.URLKey(rawURL)
	for _, s := range rows {
		if s.URL != "" && entities.URLKey(s.URL) == key {
			return s, true
		}
	}
	return entities.Source{}, false
}

// sourceByURL is FindURLSource with this use case's index.
func (uc URLs) sourceByURL(ctx context.Context, rawURL string) (entities.Source, bool) {
	return FindURLSource(ctx, uc.Index, rawURL)
}

// releaseOwnership takes a URL out of the page/sitemap/feed that had claimed it, because the human
// added it themselves. The record is written first (the truth), the index mirrors it; a nil tracker
// still clears the mirror, so the source is independent either way.
func (uc URLs) releaseOwnership(ctx context.Context, rawURL string) {
	existing, known := uc.sourceByURL(ctx, rawURL)
	if !known {
		return
	}
	if existing.Origin == "" && existing.WikiPage == "" && existing.ParentHash == "" {
		return
	}
	if uc.Steps != nil {
		if err := uc.Steps.Release(existing.Hash); err != nil && uc.Log != nil {
			uc.Log.Warn("url: the manual addition could not release the source", "url", rawURL, "err", err)
		}
	} else if err := uc.Index.ClearSourceOrigin(ctx, existing.Hash); err != nil && uc.Log != nil {
		uc.Log.Warn("url: the manual addition could not release the source", "url", rawURL, "err", err)
	}
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, existing.Hash, "info",
		"added by hand — released from its owner", map[string]any{
			"url": rawURL, "origin": existing.Origin, "wiki_page": existing.WikiPage,
			"parent_hash": existing.ParentHash,
		})
	if uc.Log != nil {
		uc.Log.Info("url: manual addition wins — the source is independent now",
			"url", rawURL, "hash", existing.Hash, "was", existing.Origin)
	}
}

// faviconHash / faviconExt expose the icon REFERENCE for the source's record: the bytes are derived
// and disposable, but the reference to them is not — without it a reindex loses the icon of every URL
// source.
func faviconHash(f *faviconMeta) string {
	if f == nil {
		return ""
	}
	return f.Hash
}

func faviconExt(f *faviconMeta) string {
	if f == nil {
		return ""
	}
	return f.Ext
}

func contentHashFromMeta(meta string) string {
	var m urlMeta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return ""
	}
	return m.ContentHash
}

func textCharsFromMeta(meta string) int {
	var m urlMeta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return 0
	}
	return m.TextChars
}

// feedCopyTextChars is how much readable text a feed copy carries. New copies record `text_chars`
// in their meta; a copy written before that field existed is measured from its snapshot. Zero means
// "unknown" and lets the page win when it is readable at all.
func feedCopyTextChars(_ context.Context, store interfaces.SourceStore, src entities.Source) int {
	if n := textCharsFromMeta(src.Meta); n > 0 {
		return n
	}
	if store == nil || src.Hash == "" || src.Ext == "" {
		return 0
	}
	rc, err := store.Open(src.Hash, src.Ext)
	if err != nil {
		return 0
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return 0
	}
	return utf8.RuneCountInString(strings.TrimSpace(string(snapshotBody(b))))
}

// snapshotBody strips the YAML frontmatter renderFeedCopySnapshot writes. It is deliberately a
// delimiter scan, not a YAML parser: the use case only needs the length of the text behind it.
func snapshotBody(b []byte) []byte {
	const open = "---\n"
	s := string(b)
	if !strings.HasPrefix(s, open) {
		return b
	}
	rest := s[len(open):]
	if _, after, ok := strings.Cut(rest, "\n---\n"); ok {
		return []byte(after)
	}
	return b
}

func metaJSON(m urlMeta) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// reading is one page as the vault will store it: the conversion, and whether a browser render
// produced it. Engine is empty for a plain HTTP read.
type reading struct {
	Conv     interfaces.ConvertedPage
	Rendered bool
	Engine   string
	Status   int
	FinalURL string
	// HTML is the document this reading was taken from: the plain response, or the rendered DOM when
	// that one won. The listing rules read the same document the text came from — a card grid a
	// script builds exists only in the DOM, and a card grid the server sends exists only in the body.
	HTML []byte
	// Wall names a gate the RENDERED page revealed and the plain response hid (a login form that
	// only exists after the scripts ran). Empty otherwise.
	Wall entities.WallKind
}

// chooseReading converts the plain response and, when the local browser is installed, converts the
// rendered DOM as well. The richer reading wins — a rendered page can LOSE text (a site whose
// scripts rewrite the article), and a vault that silently traded a complete page for a shorter one
// would be the worse kind of clever.
func (uc URLs) chooseReading(ctx context.Context, rawURL string, page interfaces.FetchedPage, minChars int) (reading, error) {
	plain, err := uc.Convert.Convert(ctx, page.Body, rawURL)
	if err != nil {
		return reading{}, err
	}
	best := reading{Conv: plain, Status: page.Status, FinalURL: page.FinalURL, HTML: page.Body}
	bestChars := utf8.RuneCountInString(strings.TrimSpace(plain.Markdown))
	if uc.Render == nil || !looksLikeHTML(page.ContentType) {
		return best, nil
	}
	rendered, renderErr := uc.Render.Render(ctx, rawURL)
	if renderErr != nil {
		// A broken page must never cost the source: the plain reading is already in hand.
		if uc.Log != nil {
			uc.Log.Warn("url: the page could not be rendered — the plain fetch is used",
				"url", rawURL, "err", renderErr)
		}
		if uc.SourceLog != nil && uc.Clock != nil {
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, entities.URLHash(rawURL), "warn",
				"the page could not be rendered — the plain fetch is used",
				map[string]any{"error": renderErr.Error()})
		}
		return best, nil
	}
	if !looksLikeHTML(rendered.ContentType) {
		return best, nil
	}
	if rendered.Challenge != "" {
		// The site answered with a bot challenge: the page is thin because the site said so, not
		// because the app failed to read it.
		if uc.Log != nil {
			uc.Log.Warn("url: the site answered with a bot challenge",
				"url", rawURL, "challenge", rendered.Challenge)
		}
		if uc.SourceLog != nil && uc.Clock != nil {
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, entities.URLHash(rawURL), "warn",
				"the site answered with a bot challenge", map[string]any{"challenge": rendered.Challenge})
		}
	}
	if rendered.Wall.Blocks() {
		// The gate was invisible without the scripts. It replaces the reading only when the plain
		// text was thin: a page that carries a real article keeps it.
		if bestChars < minChars {
			best.Wall, best.Status, best.FinalURL = rendered.Wall, rendered.Status, rendered.FinalURL
		}
		return best, nil
	}
	pageURL := rendered.FinalURL
	if pageURL == "" {
		pageURL = rawURL
	}
	conv, convErr := uc.Convert.Convert(ctx, rendered.HTML, pageURL)
	if convErr != nil {
		if uc.Log != nil {
			uc.Log.Warn("url: the rendered page could not be converted — the plain fetch is used",
				"url", rawURL, "err", convErr)
		}
		return best, nil
	}
	renderedChars := utf8.RuneCountInString(strings.TrimSpace(conv.Markdown))
	if renderedChars <= bestChars {
		// A rendered page that is not richer is still a fact about the read: the reader sees that the
		// browser was tried and what it produced, instead of a silent fallback.
		if uc.SourceLog != nil && uc.Clock != nil {
			fields := map[string]any{
				"engine": rendered.Engine, "chars": renderedChars, "plain_chars": bestChars,
			}
			if rendered.Challenge != "" {
				fields["challenge"] = rendered.Challenge
			}
			appendSourceLog(ctx, uc.SourceLog, uc.Clock, entities.URLHash(rawURL), "info",
				"the rendered page carried no more text — the plain fetch is used", fields)
		}
		if uc.Log != nil {
			uc.Log.Debug("url: the rendered DOM did not add text — the plain fetch is kept",
				"url", rawURL, "rendered_chars", renderedChars, "plain_chars", bestChars)
		}
		return best, nil
	}
	if uc.SourceLog != nil && uc.Clock != nil {
		// The reader sees WHERE the text came from: a render is a different reading of the page, not
		// an invisible improvement.
		appendSourceLog(ctx, uc.SourceLog, uc.Clock, entities.URLHash(rawURL), "info",
			"the page was read from its rendered DOM", map[string]any{
				"engine": rendered.Engine, "chars": renderedChars, "plain_chars": bestChars,
			})
	}
	return reading{
		Conv: conv, Rendered: true, Engine: rendered.Engine,
		Status: rendered.Status, FinalURL: rendered.FinalURL, HTML: rendered.HTML,
	}, nil
}

// Engine is the label the snapshot and the record carry: the engine's name when the text came from
// a rendered DOM, empty when it came from the plain response.
func (r reading) EngineLabel() string {
	if !r.Rendered {
		return ""
	}
	if r.Engine == "" {
		return "browser"
	}
	return r.Engine
}

func renderURLSnapshot(rawURL, title string, status int, fetchedAt time.Time, body, engine string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", fetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	if engine != "" {
		// Provenance, not decoration: the page was READ from a rendered DOM, and the record says so.
		fmt.Fprintf(&b, "rendered: %s\n", yamlScalar(engine))
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

// renderBookmarkSnapshot is what we keep for a link we cannot index: the metadata,
// an excerpt, and an honest note. It is never distilled.
func renderBookmarkSnapshot(rawURL, title string, conv interfaces.ConvertedPage, status int, fetchedAt time.Time, engine string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", fetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	if conv.SiteName != "" {
		fmt.Fprintf(&b, "site: %s\n", yamlScalar(conv.SiteName))
	}
	if conv.Language != "" {
		fmt.Fprintf(&b, "language: %s\n", conv.Language)
	}
	if engine != "" {
		fmt.Fprintf(&b, "rendered: %s\n", yamlScalar(engine))
	}
	b.WriteString("bookmark: true\n")
	b.WriteString("---\n\n")
	if conv.Excerpt != "" {
		b.WriteString(conv.Excerpt)
		b.WriteString("\n\n")
	}
	b.WriteString("> Collected as a link. Thought Keeper could not read enough text to index this page\n")
	b.WriteString("> (a video or a JavaScript app) — open the original to read it.\n")
	return b.String()
}

func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\"'{}[],&*?|<>=!%@`") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return strconv.Quote(s)
	}
	return s
}

// looksLikeHTML reports whether a response says it is a document to read as a page. An empty type is
// accepted: the converter already treats unknown bytes as HTML, and a detector that refused them
// would miss the half of the web that omits the header.
func looksLikeHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return ct == "" || strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}
