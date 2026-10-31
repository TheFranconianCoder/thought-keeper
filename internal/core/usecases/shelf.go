package usecases

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// SourceFavoriter writes the reader's star on a source. A handler holds this value; nil means the
// feature is not wired.
type SourceFavoriter func(ctx context.Context, hash string, on bool) (partialentities.PartialSource, error)

// SetSource sets (or clears) the star and returns the source as the wire shows it.
func (uc Favorites) SetSource(ctx context.Context, hash string, on bool) (partialentities.PartialSource, error) {
	if !entities.IsSHA256Hex(hash) {
		return partialentities.PartialSource{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	if _, err := uc.Index.GetSourceByHash(ctx, hash); err != nil {
		return partialentities.PartialSource{}, err
	}
	var at time.Time
	if on {
		at = uc.Clock.Now()
	}
	if err := uc.Shelf.SetFavorite(ctx, hash, at); err != nil {
		return partialentities.PartialSource{}, err
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	return partialentities.PartialFromSource(src), nil
}

// ReadLaterSetter puts a source in the reader's queue or finishes it. A handler holds this value; nil
// means the feature is not wired.
type ReadLaterSetter func(ctx context.Context, hash string, queued bool) (partialentities.PartialSource, error)

// Set queues, finishes, or re-opens a source. Queuing an OPEN entry is a no-op; re-queuing a finished
// one puts it at the END of the queue (the entry time is now), because a queue is worked front to
// back. It always sets the MANUAL flag: a reader who clicks the clock meant it, and a later "not this
// interest" must not take their entry away.
func (uc ReadLater) Set(ctx context.Context, hash string, queued bool) (partialentities.PartialSource, error) {
	if !entities.IsSHA256Hex(hash) {
		return partialentities.PartialSource{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	now := uc.Clock.Now()
	if queued {
		if src.ReadLaterAt.IsZero() || !src.ReadLaterDoneAt.IsZero() {
			if err := uc.Shelf.SetReadLater(ctx, hash, now, time.Time{}, true, src.ReadLaterVia); err != nil {
				return partialentities.PartialSource{}, err
			}
		}
	} else if !src.ReadLaterAt.IsZero() && src.ReadLaterDoneAt.IsZero() {
		if err := uc.Shelf.SetReadLater(ctx, hash, src.ReadLaterAt, now, src.ReadLaterManual, src.ReadLaterVia); err != nil {
			return partialentities.PartialSource{}, err
		}
	}
	fresh, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	return partialentities.PartialFromSource(fresh), nil
}

// WikiFavoriter writes the star on a wiki page. A handler holds this value; nil means the feature is
// not wired.
type WikiFavoriter func(ctx context.Context, id ulid.ULID, on bool) (entities.WikiPage, error)

// SetWiki sets (or clears) the page's star. The page file is the truth (the flag lives in its
// frontmatter); there is no index mirror, because the wiki list already reads the files.
func (uc Favorites) SetWiki(ctx context.Context, id ulid.ULID, on bool) (entities.WikiPage, error) {
	page, err := uc.Wiki.Read(ctx, id)
	if err != nil {
		return entities.WikiPage{}, err
	}
	if on {
		page.FavoriteAt = uc.Clock.Now()
	} else {
		page.FavoriteAt = time.Time{}
	}
	if err := uc.Wiki.Write(ctx, page); err != nil {
		return entities.WikiPage{}, err
	}
	return page, nil
}
