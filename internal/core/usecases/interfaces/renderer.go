package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// RenderedPage is one page as a browser sees it: the DOM after the page's own scripts ran.
//
// It is deliberately NOT Markdown. The vault has one Markdown writer (the HTML converter), so a
// rendered page goes through the same conversion as a plain HTTP response and a source does not
// change its prose style depending on how it was fetched.
type RenderedPage struct {
	Status      int
	FinalURL    string
	ContentType string
	// HTML is the serialized DOM. It is what the converter reads.
	HTML []byte
	// Wall is why the RENDERED page is not the article (a login, a paywall, a consent gate), or
	// empty. The adapter fills it from the same evidence the plain fetch uses.
	Wall entities.WallKind
	// Engine names what produced the DOM ("lightpanda"). It is written into the snapshot's
	// provenance, so a rendered source never pretends to be a plain fetch.
	Engine string
	// Challenge is the site's bot challenge the engine noticed (Cloudflare and friends), or empty.
	// It explains a thin page that is nobody's fault but the site's.
	Challenge string
}

// Renderer loads one URL in a local, credential-less browser and returns the rendered DOM. It is an
// OPTIONAL tool: nil means the platform has none (Windows has no Lightpanda build) and the plain
// fetch is the whole story.
type Renderer interface {
	Render(ctx context.Context, rawURL string) (RenderedPage, error)
}
