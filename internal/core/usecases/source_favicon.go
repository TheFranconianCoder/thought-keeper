package usecases

import (
	"context"
	"encoding/json"
	"io"

	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// FaviconOpener returns a source's favicon. A handler holds this value; nil means the feature is not
// wired.
type FaviconOpener func(ctx context.Context, hash string) (io.ReadCloser, string, error)

// Favicon serves the favicon of a source.
func (uc Sources) Favicon(ctx context.Context, hash string) (io.ReadCloser, string, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, "", err
	}
	var meta urlMeta
	if err := json.Unmarshal([]byte(src.Meta), &meta); err != nil || meta.Favicon == nil {
		return nil, "", types.New("not_found", "no favicon for this source", types.ErrNotFound)
	}
	rc, err := uc.Favicons.Open(meta.Favicon.Hash, meta.Favicon.Ext)
	if err != nil {
		return nil, "", err
	}
	return rc, contentTypeForExt(meta.Favicon.Ext), nil
}

func contentTypeForExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/x-icon"
	}
}
