package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// FetchedPage is the result of fetching a URL.
type FetchedPage struct {
	Status       int
	ContentType  string
	Body         []byte
	ETag         string
	LastModified string
	// FinalURL is where the request ended up after redirects. It is what tells "the page answered"
	// from "the site sent us to its sign-in".
	FinalURL string
	// Wall names why the response is not the page (a login, a paywall, a consent gate), or is empty
	// when it is. The adapter decides from evidence (see entities.WallKind); the use cases record it.
	Wall entities.WallKind
}

// Fetcher fetches a URL with guards (scheme, size, timeout, SSRF).
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (FetchedPage, error)
}

// ConvertedPage is a page's extracted main content plus the metadata we keep even
// when there is not enough text to index (bookmarks).
type ConvertedPage struct {
	Title    string
	Markdown string
	Excerpt  string
	SiteName string
	Language string
	// Favicon is the page's declared icon URL (may be relative).
	Favicon string
}

// HTMLConverter turns an HTML document into Markdown (main content) and metadata.
type HTMLConverter interface {
	Convert(ctx context.Context, html []byte, pageURL string) (ConvertedPage, error)
}
