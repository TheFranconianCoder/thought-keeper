package usecases

import (
	"context"
	"errors"
	"io"
	"io/fs"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// SourceOpener returns the reader plus the source's extension and title. A handler holds this value;
// nil means the feature is not wired.
type SourceOpener func(ctx context.Context, hash string) (io.ReadCloser, string, string, error)

// Content serves the content view.
func (uc Sources) Content(ctx context.Context, hash string) (io.ReadCloser, string, string, error) {
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

// SourceSidecarOpener returns one sidecar file of a source. A handler holds this value; nil means the
// feature is not wired.
type SourceSidecarOpener func(ctx context.Context, hash, suffix string) (io.ReadCloser, string, string, error)

// Sidecar serves one sidecar file.
func (uc Sources) Sidecar(ctx context.Context, hash, suffix string) (io.ReadCloser, string, string, error) {
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

// SourceRenderer returns the rendered text of a PDF or an EPUB. A handler holds this value; nil means
// the feature is not wired.
type SourceRenderer func(ctx context.Context, hash string) (io.ReadCloser, string, string, error)

// Rendered serves the rendered view.
func (uc Sources) Rendered(ctx context.Context, hash string) (io.ReadCloser, string, string, error) {
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
