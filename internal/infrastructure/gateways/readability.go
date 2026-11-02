package gateways

import (
	"context"
	"net/url"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	readability "github.com/go-shiori/go-readability"

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
	if article, err := readability.FromReader(strings.NewReader(content), parsed); err == nil {
		if strings.TrimSpace(article.Content) != "" {
			content = article.Content
		}
		readTitle = strings.TrimSpace(article.Title)
		readExcerpt = strings.TrimSpace(article.Excerpt)
		readSite = strings.TrimSpace(article.SiteName)
		readLang = strings.TrimSpace(article.Language)
		readIcon = strings.TrimSpace(article.Favicon)
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
	return out, nil
}
