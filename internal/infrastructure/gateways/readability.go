package gateways

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	readability "github.com/go-shiori/go-readability"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/dates"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// markdownConverter is the one Markdown writer of the vault. It is the package default (base +
// commonmark) PLUS the table plugin: a table is content, and the default flattened it into one
// unreadable line. The converter holds only read-only handlers after construction, so one value
// serves every capture; nothing about a single page leaks into the next.
var markdownConverter = converter.NewConverter(converter.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(),
	table.NewTablePlugin(),
))

// ReadabilityConverter extracts the main article from an HTML page (readability)
// and converts it to clean Markdown, keeping the page's metadata. Ad-hoc and local:
// no network, no daemon.
type ReadabilityConverter struct{}

var _ interfaces.HTMLConverter = ReadabilityConverter{}

// Convert returns the extracted content and metadata. The title comes from the page's
// own metadata (og:title → <title>), never from a heading; readability's title is only
// a last resort. When readability cannot find an article the raw document is converted
// instead, so a page is never lost.
func (ReadabilityConverter) Convert(_ context.Context, raw []byte, pageURL string) (interfaces.ConvertedPage, error) {
	content := string(raw)
	out := interfaces.ConvertedPage{}

	meta := extractPageMeta(raw)

	var parsed *url.URL
	if pageURL != "" {
		parsed, _ = url.Parse(pageURL) //nolint:errcheck // best effort; the caller handles the empty result
	}
	readTitle, readExcerpt, readSite, readLang, readIcon := "", "", "", "", ""
	var article readability.Article
	hasArticle := false
	if a, err := readability.FromReader(strings.NewReader(content), parsed); err == nil {
		article, hasArticle = a, true
		if strings.TrimSpace(article.Content) != "" {
			content = article.Content
		}
		readTitle = strings.TrimSpace(article.Title)
		readExcerpt = strings.TrimSpace(article.Excerpt)
		readSite = strings.TrimSpace(article.SiteName)
		readLang = strings.TrimSpace(article.Language)
		readIcon = strings.TrimSpace(article.Favicon)
	}

	// Readability keeps the page's link rails — the members who liked a post, a tag cloud, a grid of
	// the author's books. They are furniture, not the article, and distilling them writes claims
	// about usernames and counts. They are dropped here, and the original is kept when what remains is
	// too little to be a page at all (a link directory is still a page).
	if stripped := stripLinkRails(content); stripped != content {
		if md, stripErr := markdownConverter.ConvertString(stripped); stripErr == nil && railTextIsSubstantial(md) {
			content = stripped
		}
	}

	out.Title = firstNonEmpty(meta.bestTitle(), readTitle)
	out.Excerpt = firstNonEmpty(meta.bestDescription(), readExcerpt)
	out.SiteName = firstNonEmpty(meta.SiteName, readSite)
	out.Language = firstNonEmpty(meta.Language, readLang)
	out.Favicon = firstNonEmpty(meta.IconHref, readIcon)

	markdown, err := markdownConverter.ConvertString(content)
	if err != nil {
		return interfaces.ConvertedPage{}, err
	}
	out.Markdown = strings.TrimSpace(markdown)
	out.PublishedAt, out.PublishedFrom = pageDate(meta, article, hasArticle, out.Markdown)
	return out, nil
}

// pageDate picks the date the page declares, most trustworthy layer first: the metadata readability
// read (which includes JSON-LD), then the page's own meta tags and <time> elements, then a date under
// the title. The text layer is last because it is the one that can be wrong.
func pageDate(meta pageMeta, article readability.Article, hasArticle bool, markdown string) (time.Time, string) {
	if hasArticle && article.PublishedTime != nil && !article.PublishedTime.IsZero() {
		return *article.PublishedTime, entities.DateFromMeta
	}
	if !meta.Published.IsZero() {
		return meta.Published, meta.PublishedFrom
	}
	// The prose is read as the article's TEXT, not as Markdown: the writer escapes a dot ("27\.
	// Oktober") so a list would not swallow the line, and an escaped date is no date.
	text := markdown
	if hasArticle && strings.TrimSpace(article.TextContent) != "" {
		text = article.TextContent
	} else {
		text = strings.ReplaceAll(text, `\`, "")
	}
	if at, ok := dates.FromText(headRunes(text, 2000), time.Now()); ok {
		return at, entities.DateFromText
	}
	return time.Time{}, ""
}

// headRunes returns the first n runes of s: the byline area of a converted article.
func headRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
