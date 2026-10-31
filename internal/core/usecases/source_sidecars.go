package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Sidecar is one derived file of a source as the wire sees it.
type Sidecar struct {
	// Name is the suffix (".capture.md", ".meta", ".outline", ".log") — what the file IS.
	Name string
	// Bytes is its size, or -1 when it does not exist.
	Bytes int64
}

// SidecarLister lists the files a source consists of. A handler holds this value; nil means the
// feature is not wired.
type SidecarLister func(ctx context.Context, hash string) ([]Sidecar, error)

// Sidecars lists the files a source consists of.
func (uc Sources) Sidecars(ctx context.Context, hash string) ([]Sidecar, error) {
	if !entities.IsSHA256Hex(hash) {
		return nil, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	out := make([]Sidecar, 0, len(entities.SidecarSuffixes())+1)
	// A bookmark is collected, not derived: it has no claims, so it never earns a summary. Listing
	// one as missing would report a file that is not owed.
	// The object first: it is the source itself, not a sidecar.
	out = append(out, uc.sidecar(src.Hash, src.Ext, true))
	for _, suffix := range entities.SidecarSuffixes() {
		// Only a source whose bytes are not readable HAS a rendering (a PDF, an EPUB). Listing one
		// for a Markdown source would report a missing file that is not missing — it does not exist
		// because there is nothing to extract.
		if suffix == entities.SidecarRender && !src.NeedsCapture() {
			continue
		}
		if suffix == entities.SidecarSummary &&
			(src.Bookmark || src.Steps.State(entities.StepDistill) != entities.StepOK) {
			// The summary is owed only by a COMPLETE derivation: listing one for a bookmark or a
			// source still being distilled would report a file that is not missing.
			continue
		}
		out = append(out, uc.sidecar(src.Hash, suffix, false))
	}
	// The rotated older half of the log, when it exists.
	if s := uc.sidecar(src.Hash, entities.SidecarLog+".1", false); s.Bytes >= 0 {
		out = append(out, s)
	}
	return out, nil
}

func (uc Sources) sidecar(hash, suffix string, object bool) Sidecar {
	size := int64(-1)
	if object {
		if n, ok := uc.Store.StatObject(hash, suffix); ok {
			size = n
		}
	} else if n, ok := uc.Store.StatSidecar(hash, suffix); ok {
		size = n
	}
	return Sidecar{Name: suffix, Bytes: size}
}
