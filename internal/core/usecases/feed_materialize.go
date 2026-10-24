package usecases

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// feedMaterializer turns the copy of a post that a feed carries into the page's snapshot.
//
// Why it exists: many feeds carry the whole article while the page itself is a JavaScript shell
// (or behind a bot wall). Fetching such a page yields no text, so the item stayed a bookmark forever.
// The feed's copy is the readable form of the same post, and it is honest to use it — the snapshot
// says where it came from (`from_feed`, `feed`, `feed_hash`), and a later fetch that DOES produce a
// readable page replaces it (the marker is cleared then).
type feedMaterializer struct {
	Convert   interfaces.HTMLConverter
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Steps     interfaces.StepTracker
	SourceLog interfaces.SourceLog
	Languages interfaces.LanguageDetector
	Clock     interfaces.Clock
	Log       *slog.Logger
	MinChars  int
}

// expand handles a feed's whole item list:
//   - an item that already has a readable page is left alone;
//   - an item with a usable content copy (new or an unreadable bookmark) gets that copy as its
//     snapshot — no page fetch;
//   - a new item without a usable copy is claimed and returned for fetching.
//
// A KNOWN, unreadable item without a usable copy is not returned: the weekly refresh of that source
// is the place that retries the page, not every feed expansion.
func (m feedMaterializer) expand(ctx context.Context, feed entities.Source, items []feedItem) (toFetch []string, fromFeed int, err error) {
	for _, item := range items {
		link := strings.TrimSpace(item.Link)
		if link == "" {
			continue
		}
		existing, known := FindURLSource(ctx, m.Index, link)
		if known && feedCopyNotNeeded(existing) {
			continue
		}
		content := strings.TrimSpace(item.Content)

		src := existing
		if !known {
			if err := claimLink(ctx, m.Index, m.Steps, m.Clock,
				urlOwnership{Origin: entities.RSSOrigin, ParentHash: feed.Hash}, link); err != nil {
				if m.Log != nil {
					m.Log.Warn("feed: item not claimed", "url", link, "err", err)
				}
				continue
			}
			if src, err = m.Index.GetSourceByHash(ctx, entities.URLHash(link)); err != nil {
				if m.Log != nil {
					m.Log.Warn("feed: claimed item not readable", "url", link, "err", err)
				}
				continue
			}
		}

		if content != "" {
			written, wErr := m.write(ctx, src, feed, item, content)
			if wErr != nil {
				if m.Log != nil {
					m.Log.Warn("feed: copy not stored", "url", link, "err", wErr)
				}
			} else if written {
				fromFeed++
				continue
			}
		}
		if !known {
			toFetch = append(toFetch, link)
		}
	}
	return toFetch, fromFeed, nil
}

// feedCopyNotNeeded reports whether a source already has a readable page. A bookmark, a failed
// capture (a wall, a dead link) or a row without bytes can still be filled from the feed's copy.
func feedCopyNotNeeded(src entities.Source) bool {
	if src.Bookmark || src.Path == "" {
		return false
	}
	return src.Steps.State(entities.StepCapture) != entities.StepFailed
}

// write stores one item's feed copy as the page's snapshot. It returns false (and no error) when the
// copy is not readable enough: the caller then falls back to fetching the page.
func (m feedMaterializer) write(ctx context.Context, src entities.Source, feed entities.Source, item feedItem, content string) (bool, error) {
	title, body, err := m.convert(ctx, item.Link, content)
	if err != nil {
		return false, nil
	}
	body = strings.TrimSpace(body)
	if utf8.RuneCountInString(body) < m.minChars() {
		return false, nil
	}
	title = firstNonEmpty(title, item.Title, src.Title, item.Link)
	// The ADDRESS is the source's, not the item's spelling: an existing source keeps the URL it was
	// created with (the two may differ only by a trailing slash, and one page is one address).
	pageURL := firstNonEmpty(src.URL, item.Link)

	language := ""
	if m.Languages != nil {
		language = m.Languages.Detect(body)
	}
	now := m.Clock.Now()
	snapshot := renderFeedCopySnapshot(pageURL, title, feed.URL, feed.Hash, now, body)
	obj, err := m.Store.PutAt(src.Hash, ".md", []byte(snapshot))
	if err != nil {
		return false, err
	}

	src.Ext, src.Path, src.Bytes = obj.Ext, obj.Path, obj.Bytes
	src.Title, src.Bookmark, src.Language = title, false, language
	src.FromFeed, src.FeedHash = true, feed.Hash
	src.Meta = metaWithIdentity(metaWith(src.Meta, map[string]any{
		"url": pageURL, "from_feed": true, "feed_hash": feed.Hash,
		"content_hash": contentHashOf(title, body), "last_error": nil, "wall": nil,
	}), src)
	if err := m.Index.FillURLSource(ctx, src); err != nil {
		return false, err
	}
	_ = m.Index.SetSourceTitle(ctx, src.Hash, title)
	_ = m.Index.SetSourceMeta(ctx, src.Hash, src.Meta)
	// There was no HTTP answer for THESE bytes: the copy came from the feed.
	_ = m.Index.SetSourceURLState(ctx, src.Hash, 0, now)
	if language != "" {
		_ = m.Index.SetSourceLanguage(ctx, src.Hash, language)
	}
	if m.Steps != nil {
		icon := faviconFromMeta(src.Meta)
		_ = m.Steps.Reset(src.Hash)
		_ = m.Steps.SetLanguage(src.Hash, language)
		_ = m.Steps.SetIdentity(src.Hash, entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: pageURL, Language: language, OriginalName: pageURL,
			ContentHash: contentHashOf(title, body), FaviconHash: faviconHash(icon), FaviconExt: faviconExt(icon),
			Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash,
			Sitemap: src.Sitemap, RSS: src.RSS, FromFeed: true, FeedHash: feed.Hash,
		})
	}
	feedName := firstNonEmpty(feed.Title, feed.URL, "the feed")
	recordStep(ctx, m.Steps, m.Clock, src.Hash, entities.StepCapture, entities.StepOK,
		"feed copy · "+feedName, nil)
	appendSourceLog(ctx, m.SourceLog, m.Clock, src.Hash, "capture", "info", "snapshot taken from the feed",
		map[string]any{"feed": feed.URL, "feed_hash": feed.Hash, "chars": len(body)})
	return true, nil
}

// convert turns a feed item's content into Markdown. It is HTML most of the time; a plain-text copy
// is used as it is.
func (m feedMaterializer) convert(ctx context.Context, link, content string) (title, body string, err error) {
	if m.Convert != nil && strings.Contains(content, "<") {
		conv, cErr := m.Convert.Convert(ctx, []byte(content), link)
		if cErr != nil {
			return "", "", cErr
		}
		return conv.Title, conv.Markdown, nil
	}
	return "", content, nil
}

func (m feedMaterializer) minChars() int {
	if m.MinChars > 0 {
		return m.MinChars
	}
	return defaultMinTextChars
}

// renderFeedCopySnapshot is what a page's snapshot looks like when its bytes came from the feed: the
// URL it belongs to, the feed it came from, and the text. There is no http_status and no fetched_at,
// because the page itself was not fetched.
func renderFeedCopySnapshot(rawURL, title, feedURL, feedHash string, at time.Time, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "captured_at: %s\n", at.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&b, "feed: %s\n", yamlScalar(feedURL))
	fmt.Fprintf(&b, "feed_hash: %s\n", feedHash)
	b.WriteString("from_feed: true\n")
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}
