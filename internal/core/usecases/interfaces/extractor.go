package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// Extractor turns raw source bytes into ordered parts, plus what the format declares about the
// document (an EPUB's dc:title and dc:language). The extension selects the format
// (md/txt/pdf/epub). Extraction is deterministic so a reindex reproduces it.
//
// It takes the WHOLE object, not a reader: every format needs all of it anyway (the text is split
// into parts, a PDF is handed to poppler on stdin, an EPUB is a container), and a reader only ever
// invited the adapter to buffer a second private copy of bytes the caller already holds.
type Extractor interface {
	Extract(ctx context.Context, ext string, data []byte, onEvent func(entities.ExtractEvent)) (entities.Extraction, error)
}
