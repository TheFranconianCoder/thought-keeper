package interfaces

import (
	"context"
	"io"
)

// Favicon is the stored icon of a URL source.
type Favicon struct {
	Hash string
	Ext  string
}

// FaviconStore keeps a URL source's icon. It is derived, disposable data.
type FaviconStore interface {
	// Put stores the icon bytes and returns its content address.
	Put(ctx context.Context, contentType string, r io.Reader) (Favicon, error)
	// Open returns the icon bytes.
	Open(hash, ext string) (io.ReadCloser, error)
}
