package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
)

// LinkExtractor reduces an HTML page to its anchors. It is a separate port from HTMLConverter
// because the two answer different questions: the converter asks "what does this page say", the
// extractor asks "what does this page link to". A card grid is often invisible to the first and
// obvious to the second.
type LinkExtractor interface {
	// Links returns every anchor of the document, in document order, with its text, its declared
	// label, whether it sits in a heading and the structural signature of its ancestors. The rules
	// that turn links into a listing live in core/listing; the adapter only reads the document.
	Links(ctx context.Context, html []byte, pageURL string) ([]listing.Link, error)
}
