package usecases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
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
	FromFeed bool   `json:"from_feed,omitempty"`
	FeedHash string `json:"feed_hash,omitempty"`
}

// faviconMeta locates a source's stored icon.
type faviconMeta struct {
	Hash string `json:"hash"`
	Ext  string `json:"ext"`
}

// defaultMinTextChars is the amount of extracted text below which a page is kept as
// a bookmark instead of being indexed.
const defaultMinTextChars = 400

// QueueURLs claims a wiki page's NEW links. Nothing is fetched here: saving a page must stay a local
// write, so each new link becomes a source row with a record and a pending capture light, and the
// fetch happens in the background (the app's URL queue). That has two consequences, and both are the
// point:
//
//   - the link is visible in Sources from the moment the page is saved: pending, then green when the
//     page arrives, or red with the reason when it does not;
//   - a URL that already has a source is only ASSOCIATED, never fetched again — the row it already
//     owns is the answer, whatever state it is in.
type QueueURLs struct {
	Index interfaces.IndexRepository
	Steps interfaces.StepTracker
	Clock interfaces.Clock
	Log   *slog.Logger
}

// urlOwnership is who controls a dependent URL source. A wiki page and a sitemap are the two
// owners: deleting the owner removes the URLs it brought into the vault.
type urlOwnership struct {
	Origin     string
	WikiPage   string
	ParentHash string
}

// Execute records every URL that has no source yet and returns exactly those: they are the links the
// caller has to fetch. URLs a source already owns are left alone.
func (uc QueueURLs) Execute(ctx context.Context, pageID string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.WikiLinkOrigin, WikiPage: pageID}, urls)
}

// ExecuteSitemap claims the pages a sitemap declared. Their owner is the sitemap source itself, so
// removing the sitemap removes every page that exists only because of it.
func (uc QueueURLs) ExecuteSitemap(ctx context.Context, parentHash string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.SitemapOrigin, ParentHash: parentHash}, urls)
}

// ExecuteFeed claims the pages a feed declared. The feed owns them the same way a sitemap does; the
// difference is the REFRESH, which keeps a page that disappears from a feed.
func (uc QueueURLs) ExecuteFeed(ctx context.Context, parentHash string, urls []string) ([]string, error) {
	return uc.queue(ctx, urlOwnership{Origin: entities.RSSOrigin, ParentHash: parentHash}, urls)
}

func (uc QueueURLs) queue(ctx context.Context, owner urlOwnership, urls []string) ([]string, error) {
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
	_ = steps.SetIdentity(hash, entities.SourceIdentity{
		Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
		Origin: owner.Origin, WikiPage: owner.WikiPage, ParentHash: owner.ParentHash,
	})
	_ = steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{
		State: entities.StepPending, At: now, Detail: "queued: the link is fetched in the background",
	})
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
		_ = steps.Record(ctx, hash, step, entities.StepStatus{
			State: entities.StepSkipped, At: now, Detail: "the link has not been fetched yet",
		})
	}
	return nil
}

// MarkURLFailed records a link whose fetch did not happen. A dead link stays LISTED — it is part of
// what the page claims — with a red capture light and the reason in its record, its metadata and its
// own log, instead of disappearing into the process log the reader never opens.
type MarkURLFailed struct {
	Index     interfaces.IndexRepository
	Steps     interfaces.StepTracker
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	Log       *slog.Logger
}

// Execute marks the URL's source failed and returns its hash.
func (uc MarkURLFailed) Execute(ctx context.Context, rawURL, pageID string, cause error) (string, error) {
	return uc.ExecuteOwned(ctx, rawURL, urlOwnership{Origin: entities.WikiLinkOrigin, WikiPage: pageID}, cause)
}

// ExecuteSitemap records a failure for a page that a sitemap owns.
func (uc MarkURLFailed) ExecuteSitemap(ctx context.Context, rawURL, parentHash string, cause error) (string, error) {
	return uc.ExecuteOwned(ctx, rawURL, urlOwnership{Origin: entities.SitemapOrigin, ParentHash: parentHash}, cause)
}

// ExecuteFeed records a failure for a page that a feed owns.
func (uc MarkURLFailed) ExecuteFeed(ctx context.Context, rawURL, parentHash string, cause error) (string, error) {
	return uc.ExecuteOwned(ctx, rawURL, urlOwnership{Origin: entities.RSSOrigin, ParentHash: parentHash}, cause)
}

// ExecuteOwned records a failure for a URL with an explicit owner (a sitemap uses this; a wiki page
// uses Execute above).
func (uc MarkURLFailed) ExecuteOwned(ctx context.Context, rawURL string, owner urlOwnership, cause error) (string, error) {
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
	_ = uc.Index.SetSourceURLState(ctx, hash, 0, now)
	_ = uc.Index.SetSourceMeta(ctx, hash, metaWithError(src.Meta, rawURL, reason))
	if uc.Steps != nil {
		_ = uc.Steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{
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

// metaWith merges keys into a source's metadata WITHOUT dropping the keys it does not know. The same
// record also holds the language and the wiki page that owns the link, and a snapshot write that
// rebuilt the JSON from urlMeta used to erase both — with the origin went the answer to "may removing
// the page's link remove this source?". A nil value removes the key.
func metaWith(raw string, set map[string]any) string {
	m := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
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

// AddURL fetches a URL and stores it as an indexed Markdown snapshot, or — when the
// page cannot be read (video, paywall, JS app) — as a **bookmark**: metadata only,
// never distilled. Both live in the same content-addressed store, so the collection
// stays one.
type AddURL struct {
	Fetch    interfaces.Fetcher
	Convert  interfaces.HTMLConverter
	Store    interfaces.SourceStore
	Index    interfaces.IndexRepository
	Favicons interfaces.FaviconStore
	// MinTextChars overrides defaultMinTextChars.
	MinTextChars int
	Clock        interfaces.Clock
	Log          *slog.Logger
	// Steps records the CAPTURE step (and, for a bookmark, that outline and distill do not apply).
	Steps interfaces.StepTracker
	// SourceLog receives the one line that says a sitemap expanded to N pages.
	SourceLog interfaces.SourceLog
	// Languages recognises the page's language. Nil means no detection.
	Languages interfaces.LanguageDetector
}

// Execute fetches and stores one URL that a wiki page, sitemap or feed claimed. It never takes a
// source away from its owner. force=true stores a bookmark regardless of how much text the page has.
func (uc AddURL) Execute(ctx context.Context, rawURL string, force bool) (partialentities.PartialSource, error) {
	return uc.execute(ctx, rawURL, force, false)
}

// ExecuteManual fetches a URL the HUMAN added by hand. A manual addition WINS: if the URL was already
// in the vault as a sub-source of a wiki page, a sitemap or a feed, it is released from that owner, so
// deleting the page or the sitemap no longer deletes it. The source itself (snapshot, claims) stays.
func (uc AddURL) ExecuteManual(ctx context.Context, rawURL string, force bool) (partialentities.PartialSource, error) {
	return uc.execute(ctx, rawURL, force, true)
}

func (uc AddURL) execute(ctx context.Context, rawURL string, force, manual bool) (partialentities.PartialSource, error) {
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
	conv, err := uc.Convert.Convert(ctx, page.Body, rawURL)
	if err != nil {
		return partialentities.PartialSource{}, fmt.Errorf("convert %s: %w", rawURL, err)
	}
	title := conv.Title
	if title == "" {
		title = rawURL
	}

	minChars := uc.MinTextChars
	if minChars <= 0 {
		minChars = defaultMinTextChars
	}
	body := strings.TrimSpace(conv.Markdown)
	bookmark := force || utf8.RuneCountInString(body) < minChars
	language := ""
	if uc.Languages != nil {
		language = uc.Languages.Detect(body)
		if language == "" && !bookmark {
			language = uc.Languages.Detect(title + "\n" + conv.Excerpt)
		}
	}

	now := uc.Clock.Now()
	favicon := fetchFavicon(ctx, uc.Fetch, uc.Favicons, uc.Log, rawURL, conv.Favicon)
	var snapshot, contentHash string
	if bookmark {
		snapshot = renderBookmarkSnapshot(rawURL, title, conv, page.Status, now)
		contentHash = contentHashOf(title, snapshot)
	} else {
		snapshot = renderURLSnapshot(rawURL, title, page.Status, now, body)
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
	existing, known := uc.sourceByURL(ctx, rawURL)
	// A page that still does not read must not overwrite the readable copy the FEED supplied (the
	// page is a JavaScript shell, a bot wall); the weekly refresh keeps trying it.
	if bookmark && known && isFromFeed(existing.Meta) {
		_ = uc.Index.SetSourceURLState(ctx, existing.Hash, page.Status, now)
		if uc.Log != nil {
			uc.Log.Info("url: the page is still not readable — the feed's copy is kept", "url", rawURL)
		}
		stored, err := uc.Index.GetSourceByHash(ctx, existing.Hash)
		if err != nil {
			stored = existing
		}
		return partialentities.PartialFromSource(stored), nil
	}
	if known && existing.Path != "" {
		if _, err := uc.Store.PutAt(existing.Hash, ".md", []byte(snapshot)); err != nil {
			return partialentities.PartialSource{}, err
		}
		existing.Ext, existing.Path, existing.Bytes = obj.Ext, obj.Path, obj.Bytes
		existing.Language = language
		// A readable page replaces the feed's copy: the marker goes with it.
		existing.FromFeed, existing.FeedHash = false, ""
		existing.Title, existing.Meta = title, metaWithIdentity(metaJSON(urlMeta{
			ContentHash: contentHash, Bookmark: bookmark, Excerpt: conv.Excerpt,
			Site: conv.SiteName, URL: rawURL, Favicon: favicon,
		}), existing)
		if err := uc.Index.UpsertSource(ctx, existing); err != nil {
			return partialentities.PartialSource{}, err
		}
		_ = uc.Index.SetSourceTitle(ctx, existing.Hash, title)
		_ = uc.Index.SetSourceMeta(ctx, existing.Hash, existing.Meta)
		_ = uc.Index.SetSourceURLState(ctx, existing.Hash, page.Status, now)
		if language != "" {
			_ = uc.Index.SetSourceLanguage(ctx, existing.Hash, language)
			if uc.Steps != nil {
				_ = uc.Steps.SetLanguage(existing.Hash, language)
			}
		}
		recordStep(ctx, uc.Steps, uc.Clock, existing.Hash, entities.StepCapture, entities.StepOK,
			fmt.Sprintf("HTTP %d · refetched", page.Status), nil)
		if uc.Steps != nil {
			_ = uc.Steps.SetIdentity(existing.Hash, entities.SourceIdentity{
				Kind: entities.SourceKindURL, URL: rawURL, Bookmark: bookmark, Language: language,
				OriginalName: rawURL, ContentHash: contentHash, FaviconHash: faviconHash(favicon),
				FaviconExt: faviconExt(favicon), Origin: existing.Origin, WikiPage: existing.WikiPage,
				ParentHash: existing.ParentHash, Sitemap: existing.Sitemap, RSS: existing.RSS,
			})
		}
		stored, err := uc.Index.GetSourceByHash(ctx, existing.Hash)
		if err != nil {
			stored = existing
		}
		return partialentities.PartialFromSource(stored), nil
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
			Site: conv.SiteName, URL: rawURL, Favicon: favicon,
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
			_ = uc.Steps.Reset(src.Hash)
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
		_ = uc.Index.SetSourceLanguage(ctx, src.Hash, language)
		if uc.Steps != nil {
			_ = uc.Steps.SetLanguage(src.Hash, language)
		}
	}
	if err := uc.Index.SetSourceURLState(ctx, src.Hash, page.Status, now); err != nil {
		return partialentities.PartialSource{}, err
	}
	if bookmark {
		// One outline node makes the bookmark findable (source_nodes_fts) while the
		// sweep keeps ignoring it (sources.bookmark = 1).
		_ = uc.Index.ReplaceSourceNodes(ctx, src.ID, []entities.SourceNode{{
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
	detail := fmt.Sprintf("%s · HTTP %d", conv.SiteName, page.Status)
	if bookmark {
		detail = fmt.Sprintf("bookmark · HTTP %d", page.Status)
	}
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK, detail, nil)
	// A URL snapshot is a .md — exactly like a Markdown source, and like a PDF's readable rendering.
	// What the source IS therefore lives in its record: without it a reindex would call this source
	// Markdown and lose the address it came from.
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: rawURL, Bookmark: bookmark, Language: language,
			OriginalName: rawURL, ContentHash: contentHash, FaviconHash: faviconHash(favicon),
			FaviconExt: faviconExt(favicon), Origin: src.Origin, WikiPage: src.WikiPage,
			ParentHash: src.ParentHash, Sitemap: src.Sitemap, RSS: src.RSS,
		})
	}
	if bookmark {
		// A bookmark is collected, not indexed: outline and distill do not apply to it, and saying
		// so is better than leaving two lights pending forever.
		recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "bookmark", nil)
		recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "bookmark", nil)
	}
	return partialentities.PartialFromSource(stored), nil
}

// storeSitemap writes the sitemap as a source of its own and returns the URLs it declared. The
// caller claims those URLs with this source as their owner; nothing is fetched here.
//
// A sitemap is not a bookmark: it is the parent of the pages it lists. It is not distilled either
// (its body is a URL list), so outline and distill are recorded as skipped with that reason.
func (uc AddURL) storeSitemap(ctx context.Context, rawURL string, page interfaces.FetchedPage, entries []string) (partialentities.PartialSource, error) {
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
			_ = uc.Steps.Reset(src.Hash)
		}
	}
	if known {
		if err := uc.Index.FillURLSource(ctx, src); err != nil {
			return partialentities.PartialSource{}, err
		}
	} else if err := uc.Index.UpsertSource(ctx, src); err != nil {
		return partialentities.PartialSource{}, err
	}
	_ = uc.Index.SetSourceURLState(ctx, src.Hash, page.Status, now)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK,
		fmt.Sprintf("HTTP %d · sitemap · %d URLs", page.Status, len(entries)), nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "sitemap", nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "sitemap", nil)
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{
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
func (uc AddURL) storeFeed(ctx context.Context, rawURL string, page interfaces.FetchedPage, doc feedDocument) (partialentities.PartialSource, error) {
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
			_ = uc.Steps.Reset(src.Hash)
		}
	}
	if known {
		if err := uc.Index.FillURLSource(ctx, src); err != nil {
			return partialentities.PartialSource{}, err
		}
	} else if err := uc.Index.UpsertSource(ctx, src); err != nil {
		return partialentities.PartialSource{}, err
	}
	_ = uc.Index.SetSourceURLState(ctx, src.Hash, page.Status, now)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK,
		fmt.Sprintf("HTTP %d · feed · %d items", page.Status, len(doc.Items)), nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepOutline, entities.StepSkipped, "feed", nil)
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepDistill, entities.StepSkipped, "feed", nil)
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
			Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash,
			RSS: true, ContentHash: contentHash,
		})
	}
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "capture", "info", "feed expanded",
		map[string]any{"url": rawURL, "items": len(doc.Items)})
	// The items are claimed and, where the feed carries the post, filled from that copy. Only the
	// rest need a page fetch — a JavaScript page would otherwise stay a bookmark forever.
	materializer := feedMaterializer{
		Convert: uc.Convert, Store: uc.Store, Index: uc.Index, Steps: uc.Steps,
		SourceLog: uc.SourceLog, Languages: uc.Languages, Clock: uc.Clock, Log: uc.Log,
		MinChars: uc.MinTextChars,
	}
	toFetch, fromFeed, expandErr := materializer.expand(ctx, src, doc.Items)
	if expandErr != nil {
		return partialentities.PartialSource{}, expandErr
	}
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "capture", "info", "feed items handled",
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
func (uc AddURL) recordWall(ctx context.Context, rawURL string, page interfaces.FetchedPage) (partialentities.PartialSource, error) {
	now := uc.Clock.Now()
	existing, known := uc.sourceByURL(ctx, rawURL)
	if known && existing.Path != "" && wallOf(existing.Meta) == entities.WallNone {
		_ = uc.Index.SetSourceURLState(ctx, existing.Hash, page.Status, now)
		_ = uc.Index.SetSourceMeta(ctx, existing.Hash, metaWithIdentity(metaWith(existing.Meta, map[string]any{
			"url": rawURL, "wall": string(page.Wall), "last_error": page.Wall.Reason(),
		}), existing))
		appendSourceLog(ctx, uc.SourceLog, uc.Clock, existing.Hash, "capture", "warn",
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
	_ = uc.Index.SetSourceTitle(ctx, written.Hash, written.Title)
	_ = uc.Index.SetSourceMeta(ctx, written.Hash, written.Meta)
	if err := uc.Index.SetSourceURLState(ctx, written.Hash, page.Status, now); err != nil {
		return partialentities.PartialSource{}, err
	}
	if uc.Steps != nil {
		_ = uc.Steps.SetIdentity(written.Hash, entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: rawURL, OriginalName: rawURL,
			Origin: written.Origin, WikiPage: written.WikiPage, ParentHash: written.ParentHash,
			Sitemap: written.Sitemap, RSS: written.RSS,
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
		_ = w.Steps.Record(ctx, src.Hash, entities.StepCapture, entities.StepStatus{
			State: entities.StepFailed, At: now, Detail: page.Wall.Label(), Error: page.Wall.Reason(),
		})
		for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
			_ = w.Steps.Record(ctx, src.Hash, step, entities.StepStatus{
				State: entities.StepSkipped, At: now, Detail: "blocked: " + page.Wall.Label(),
			})
		}
	}
	appendSourceLog(ctx, w.SourceLog, w.Clock, src.Hash, "capture", "warn", "the page is walled",
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

// isFromFeed reports whether the snapshot came from the feed's copy of the post.
func isFromFeed(meta string) bool {
	var m urlMeta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return false
	}
	return m.FromFeed
}

// appendSourceLog writes one line into a source's own log (best-effort: a log never fails a write).
func appendSourceLog(ctx context.Context, log interfaces.SourceLog, clock interfaces.Clock, hash, phase, level, msg string, fields map[string]any) {
	if log == nil {
		return
	}
	log.Append(ctx, hash, interfaces.SourceLogEvent{
		At: clock.Now(), Phase: phase, Level: level, Message: msg, Fields: fields,
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

// RefreshURLs re-fetches every known URL once. An unchanged page only gets its
// status refreshed; a changed page becomes a new snapshot and source (the old
// snapshot stays — history is immutable), which the next document run distills.
// Atoms that cite the replaced snapshot are marked superseded, so stale claims are
// visible rather than silently duplicated.
type RefreshURLs struct {
	Fetch   interfaces.Fetcher
	Convert interfaces.HTMLConverter
	Store   interfaces.SourceStore
	Index   interfaces.IndexRepository
	// Favicons is where a URL source's icon lives. A refresh of a changed page carries it over (and
	// refreshes it when the site offers one): an icon is not something a re-fetch may lose.
	Favicons interfaces.FaviconStore
	Atoms    interfaces.AtomRepository
	Clock    interfaces.Clock
	Log      *slog.Logger
	// Steps records the CAPTURE step and the identity of a CHANGED page's new snapshot: it is a new
	// source, and it must be as recognizable as the one it replaces.
	Steps interfaces.StepTracker
	// SourceLog receives the line that says a page was walled (a login, a paywall) instead of read.
	SourceLog interfaces.SourceLog
	// Languages refreshes the language hint when a page changes.
	Languages interfaces.LanguageDetector
	// MinTextChars is the amount of text below which a fetch is not allowed to replace a snapshot
	// that came from a feed's copy (see feedMaterializer). Zero means defaultMinTextChars.
	MinTextChars int
	// Delete removes a page that disappeared from a SITEMAP (which is a complete list). A feed is not:
	// its items are a window, so a page that leaves it is kept. Zero value = no deletion (tests).
	Delete DeleteSource
	// MinAge is the minimum time since a source's last check before a pass re-fetches it. Zero means
	// no limit. The cadence belongs to the SOURCE (`url_checked_at`), not to the process's uptime:
	// with a plain ticker, restarting the app daily would mean a weekly refresh never happens.
	MinAge time.Duration
}

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
	// Discovered are the new children sitemaps and feeds declared.
	Discovered []DiscoveredChildren
	// Removed are the pages a SITEMAP no longer lists. A feed never removes an item.
	Removed int
	Sources []partialentities.PartialSource
	// ChangedURLs are the pages that actually changed, so the caller knows whether the association
	// layer has something to regroup.
	ChangedURLs []string
	// LastError is why the last source that failed did so. The counts are enough for a pass; the
	// per-source Refresh button has one row to explain, and "1 failed" alone is not an explanation.
	LastError string
}

// Execute refreshes the URL sources that are DUE (a scheduled pass: `MinAge` decides).
func (uc RefreshURLs) Execute(ctx context.Context) (RefreshResult, error) {
	return uc.execute(ctx, uc.MinAge)
}

// ExecuteAll refreshes every known URL now, whatever its age. It is the manual pass — the Status
// button — and the only caller that is allowed to ignore the cadence.
func (uc RefreshURLs) ExecuteAll(ctx context.Context) (RefreshResult, error) {
	return uc.execute(ctx, 0)
}

// ExecuteOne refreshes ONE URL source now, whatever its age. It is the per-source Refresh button: a
// feed or a sitemap is a list that only changes when it is fetched again, and a page may be checked
// without waiting for the weekly cadence.
func (uc RefreshURLs) ExecuteOne(ctx context.Context, hash string) (RefreshResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return RefreshResult{}, err
	}
	if src.URL == "" {
		return RefreshResult{}, types.New("unsupported_source",
			"this source has no URL to refresh", types.ErrUnsupportedSource)
	}
	return uc.executeRows(ctx, []entities.Source{src}, 0)
}

func (uc RefreshURLs) execute(ctx context.Context, minAge time.Duration) (RefreshResult, error) {
	rows, err := uc.Index.ListURLSources(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	return uc.executeRows(ctx, rows, minAge)
}

func (uc RefreshURLs) executeRows(ctx context.Context, rows []entities.Source, minAge time.Duration) (RefreshResult, error) {
	var res RefreshResult
	seen := make(map[string]bool)
	for _, s := range rows {
		if s.URL == "" || seen[entities.URLKey(s.URL)] {
			continue
		}
		seen[entities.URLKey(s.URL)] = true
		// A snapshot the human attached is not checked at all. It exists BECAUSE the page cannot be
		// fetched; asking the site every week would only write a 403 onto an article that is fine.
		if isAttached(s.Meta) {
			res.Attached++
			continue
		}
		now := uc.Clock.Now()
		// Due means "not checked within the cadence". A URL that was never checked is always due.
		if minAge > 0 && !s.URLChecked.IsZero() && now.Sub(s.URLChecked) < minAge {
			continue
		}
		res.Checked++
		page, fetchErr := uc.Fetch.Fetch(ctx, s.URL)
		if fetchErr != nil {
			res.Failed++
			res.LastError = fetchErr.Error()
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, 0, now)
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
					_ = uc.Index.SetSourceTitle(ctx, s.Hash, written.Title)
					_ = uc.Index.SetSourceMeta(ctx, s.Hash, written.Meta)
					if uc.Steps != nil {
						_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{
							Kind: entities.SourceKindURL, URL: s.URL, OriginalName: s.URL,
							Origin: s.Origin, WikiPage: s.WikiPage, ParentHash: s.ParentHash,
						})
					}
				}
			} else {
				_ = uc.Index.SetSourceMeta(ctx, s.Hash, metaWithIdentity(metaWith(s.Meta, map[string]any{
					"url": s.URL, "wall": string(page.Wall), "last_error": page.Wall.Reason(),
				}), s))
			}
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
			if uc.Log != nil {
				uc.Log.Info("url refresh: blocked", "url", s.URL, "wall", string(page.Wall), "status", page.Status)
			}
			continue
		}
		if page.Status < 200 || page.Status >= 300 {
			res.Failed++
			res.LastError = fmt.Sprintf("HTTP %d", page.Status)
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
			if uc.Log != nil {
				uc.Log.Info("url refresh: not ok", "url", s.URL, "status", page.Status)
			}
			continue
		}
		// A sitemap's or feed's snapshot is a LIST, not HTML: re-expanding it here is what brings new
		// pages in (and, for a sitemap, what removes the ones it no longer lists). Its children are
		// fetched as their own sources.
		if s.Sitemap || s.RSS {
			parent, queued, removed, parentErr := uc.refreshParent(ctx, s, page, now)
			if parentErr != nil {
				res.Failed++
				res.LastError = parentErr.Error()
				_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
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

		conv, convErr := uc.Convert.Convert(ctx, page.Body, s.URL)
		if convErr != nil {
			res.Failed++
			res.LastError = convErr.Error()
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
			continue
		}
		body := strings.TrimSpace(conv.Markdown)
		title := conv.Title
		if title == "" {
			title = s.URL
		}

		// A copy taken from a FEED must not be replaced by a page that still does not read (a
		// JavaScript shell, a bot wall): the feed's copy is the readable form of the same post. The
		// weekly pass tries the page again; when it does yield a readable page, that page wins.
		if isFromFeed(s.Meta) {
			minChars := uc.MinTextChars
			if minChars <= 0 {
				minChars = defaultMinTextChars
			}
			if utf8.RuneCountInString(body) < minChars {
				_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
				if uc.Log != nil {
					uc.Log.Info("url refresh: the page is still not readable — the feed's copy is kept",
						"url", s.URL, "chars", utf8.RuneCountInString(body))
				}
				continue
			}
		}

		// A bookmark stays a bookmark: only its status is refreshed.
		if s.Bookmark {
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
			if uc.Log != nil {
				uc.Log.Debug("bookmark refreshed", "url", s.URL, "status", page.Status)
			}
			continue
		}
		if body == "" {
			res.Failed++
			res.LastError = "the page gave no readable text"
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
			continue
		}

		language := ""
		if uc.Languages != nil {
			language = uc.Languages.Detect(body)
		}
		if language != "" {
			s.Language = language
			_ = uc.Index.SetSourceLanguage(ctx, s.Hash, language)
			if uc.Steps != nil {
				_ = uc.Steps.SetLanguage(s.Hash, language)
			}
		}

		contentHash := contentHashOf(title, body)
		// A URL record written before the identity carried the icon has no favicon reference. A
		// refresh is the natural repair: fetch it, write it into the index AND the source record,
		// then the next reindex cannot lose it again.
		favicon := faviconFromMeta(s.Meta)
		if favicon == nil {
			favicon = fetchFavicon(ctx, uc.Fetch, uc.Favicons, uc.Log, s.URL, conv.Favicon)
			if favicon != nil {
				s.Meta = metaWithFavicon(s.Meta, s.URL, contentHash, favicon)
				_ = uc.Index.SetSourceMeta(ctx, s.Hash, s.Meta)
				if language != "" {
					_ = uc.Index.SetSourceLanguage(ctx, s.Hash, language)
				}
				if uc.Steps != nil {
					_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{
						Kind: entities.SourceKindURL, URL: s.URL, Bookmark: s.Bookmark, Language: language,
						OriginalName: s.OriginalName, ContentHash: contentHash,
						FaviconHash: faviconHash(favicon), FaviconExt: faviconExt(favicon),
						FromFeed: s.FromFeed, FeedHash: s.FeedHash,
					})
				}
			}
		}
		if contentHash == contentHashFromMeta(s.Meta) {
			// Same content: just record that we checked and the page still answers.
			_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
			if uc.Log != nil {
				uc.Log.Debug("url unchanged", "url", s.URL, "status", page.Status)
			}
			continue
		}

		snapshot := renderURLSnapshot(s.URL, title, page.Status, now, body)
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
		// A readable page replaces the feed's copy: the snapshot is now the page's, so the marker goes.
		meta = metaWith(meta, map[string]any{"from_feed": nil, "feed_hash": nil})
		updated := s
		updated.Ext, updated.Path, updated.Bytes = obj.Ext, obj.Path, obj.Bytes
		updated.Title, updated.Meta = title, meta
		updated.Language = language
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
		if language != "" {
			_ = uc.Index.SetSourceLanguage(ctx, s.Hash, language)
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
			_ = uc.Steps.Reset(s.Hash)
		}
		recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK,
			fmt.Sprintf("HTTP %d · refetched", page.Status), nil)
		if uc.Steps != nil {
			_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{
				Kind: entities.SourceKindURL, URL: s.URL, Language: language, OriginalName: s.URL,
				ContentHash: contentHash, FaviconHash: faviconHash(favicon), FaviconExt: faviconExt(favicon),
			})
		}
		res.Changed++
		res.Sources = append(res.Sources, partialentities.PartialFromSource(updated))
		res.ChangedURLs = append(res.ChangedURLs, s.URL)
		if uc.Log != nil {
			uc.Log.Info("url changed", "url", s.URL, "hash", s.Hash, "markdown_bytes", len(body))
		}
	}
	return res, nil
}

// refreshParent re-expands a sitemap or a feed and keeps its children in step with it. The difference
// is what the list PROMISES: a sitemap says "these are the pages", so a page that left it is deleted
// (a 1:1 reconciliation); a feed says "these are the newest items", so a page that left it is kept —
// the archive of what the feed announced is the point. Only deleting the owner removes them.
func (uc RefreshURLs) refreshParent(ctx context.Context, s entities.Source, page interfaces.FetchedPage, now time.Time) (partialentities.PartialSource, []string, int, error) {
	queue := QueueURLs{Index: uc.Index, Steps: uc.Steps, Clock: uc.Clock, Log: uc.Log}
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
		if queued, err = queue.ExecuteSitemap(ctx, s.Hash, entries); err != nil {
			return partialentities.PartialSource{}, nil, 0, err
		}
		if removed, err = uc.removeGoneChildren(ctx, s, entries); err != nil {
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
		if queued, fromFeed, err = materializer.expand(ctx, s, doc.Items); err != nil {
			return partialentities.PartialSource{}, nil, 0, err
		}
		detail = fmt.Sprintf("HTTP %d · feed · %d items · %d from feed", page.Status, len(doc.Items), fromFeed)
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
	_ = uc.Index.SetSourceTitle(ctx, s.Hash, title)
	_ = uc.Index.SetSourceMeta(ctx, s.Hash, updated.Meta)
	_ = uc.Index.SetSourceURLState(ctx, s.Hash, page.Status, now)
	if uc.Steps != nil {
		icon := faviconFromMeta(updated.Meta)
		_ = uc.Steps.SetIdentity(s.Hash, entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: s.URL, Language: s.Language, OriginalName: s.URL,
			ContentHash: contentHash, FaviconHash: faviconHash(icon), FaviconExt: faviconExt(icon),
			Origin: s.Origin, WikiPage: s.WikiPage, ParentHash: s.ParentHash,
			Sitemap: s.Sitemap, RSS: s.RSS,
		})
	}
	recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepCapture, entities.StepOK, detail, nil)
	// A feed or a sitemap is a LIST: the refresh just fetched it, and the document steps still do not
	// apply. Re-recording them keeps the .meta the truth even when an older record lost them.
	parentDetail := "feed"
	if s.Sitemap {
		parentDetail = "sitemap"
	}
	recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepOutline, entities.StepSkipped, parentDetail, nil)
	recordStep(ctx, uc.Steps, uc.Clock, s.Hash, entities.StepDistill, entities.StepSkipped, parentDetail, nil)
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, s.Hash, "capture", "info", "expanded again", map[string]any{
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
func (uc RefreshURLs) removeGoneChildren(ctx context.Context, parent entities.Source, entries []string) (int, error) {
	if uc.Delete.Index == nil {
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
	removed := 0
	for _, child := range children {
		if child.ParentHash != parent.Hash || child.Origin != entities.SitemapOrigin {
			continue
		}
		if child.URL != "" && keep[entities.URLKey(child.URL)] {
			continue
		}
		if _, err := uc.Delete.ExecuteOwned(ctx, child.Hash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("sitemap refresh: page not removed", "sitemap", parent.Hash,
					"page", child.Hash, "err", err)
			}
			continue
		}
		removed++
	}
	return removed, nil
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
func (uc AddURL) sourceByURL(ctx context.Context, rawURL string) (entities.Source, bool) {
	return FindURLSource(ctx, uc.Index, rawURL)
}

// releaseOwnership takes a URL out of the page/sitemap/feed that had claimed it, because the human
// added it themselves. The record is written first (the truth), the index mirrors it; a nil tracker
// still clears the mirror, so the source is independent either way.
func (uc AddURL) releaseOwnership(ctx context.Context, rawURL string) {
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
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, existing.Hash, "capture", "info",
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

func metaJSON(m urlMeta) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func renderURLSnapshot(rawURL, title string, status int, fetchedAt time.Time, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", fetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

// renderBookmarkSnapshot is what we keep for a link we cannot index: the metadata,
// an excerpt, and an honest note. It is never distilled.
func renderBookmarkSnapshot(rawURL, title string, conv interfaces.ConvertedPage, status int, fetchedAt time.Time) string {
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
