package usecases

import (
	"context"
	"errors"
	"io"
	"io/fs"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// SourceContent opens a source's raw bytes for inline viewing (PDF, Markdown, text).
// The bytes are the immutable snapshot from the content-addressed store.
type SourceContent struct {
	Index interfaces.IndexRepository
	Store interfaces.SourceStore
}

// Execute returns the reader plus the source's extension and title.
func (uc SourceContent) Execute(ctx context.Context, hash string) (io.ReadCloser, string, string, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, "", "", err
	}
	rc, err := uc.Store.Open(hash, src.Ext)
	if err != nil {
		return nil, "", "", err
	}
	return rc, src.Ext, src.Title, nil
}

// Sidecar opens one DERIVED file of a source for inline viewing: its step record, its outline or its
// log. The caller passes one of the fixed suffixes the Files view shows (entities.Sidecar*) — never a
// path, and nothing outside this source's folder can be named: the store resolves the name next to the
// object, and the switch below is the whitelist.
func (uc SourceContent) Sidecar(ctx context.Context, hash, suffix string) (io.ReadCloser, string, string, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, "", "", err
	}
	switch suffix {
	case entities.SidecarMeta, entities.SidecarOutline, entities.SidecarSummary, entities.SidecarLog:
	default:
		return nil, "", "", types.New("not_found",
			"this source has no file named "+suffix, types.ErrNotFound)
	}
	rc, err := uc.Store.OpenRendered(hash, suffix)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", "", types.New("not_found",
				"this source has no file named "+suffix+" yet", types.ErrNotFound)
		}
		return nil, "", "", err
	}
	return rc, suffix, src.Title, nil
}

// Rendered opens a source's READABLE rendering: the Markdown that step 1 wrote next to a source
// whose bytes are not readable as they are (a PDF, an EPUB).
//
// It exists for the reader. A PDF is shown in the overlay, and loading a 50 MB document into a frame
// is not instant — the rendering is the text the pipeline itself read (the outline parses exactly
// this file), so showing it is both honest about what the system saw and immediate. A kind whose
// bytes ARE readable has no rendering: there the object is the text.
func (uc SourceContent) Rendered(ctx context.Context, hash string) (io.ReadCloser, string, string, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, "", "", err
	}
	if !src.NeedsCapture() {
		return nil, "", "", types.New("not_found",
			"this source has no rendering: its bytes are readable as they are", types.ErrNotFound)
	}
	rc, err := uc.Store.OpenRendered(hash, entities.SidecarRender)
	if err != nil {
		// A rendering that is not there yet (capture pending, or an extraction that produced
		// nothing) is a missing file, not a server failure: the reader is told what it is.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", "", types.New("not_found",
				"this source has no readable rendering yet: it has not been captured", types.ErrNotFound)
		}
		return nil, "", "", err
	}
	return rc, entities.SidecarRender, src.Title, nil
}
