package usecases

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// SetSourceFavorite writes the reader's star. A favorite is not a tag and not a claim: it is the
// only mark that says "this belongs to me", it has no lifecycle, and it never feeds search or the
// association layer. The source's <hash>.meta record is the truth; the use case mirrors it.
type SetSourceFavorite struct {
	Index interfaces.IndexRepository
	Shelf interfaces.ShelfStore
	Clock interfaces.Clock
}

// Execute sets (or clears) the star and returns the source as the wire shows it.
func (uc SetSourceFavorite) Execute(ctx context.Context, hash string, on bool) (partialentities.PartialSource, error) {
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

// SetSourceReadLater puts a source in the reader's queue or finishes it. It always sets the MANUAL
// flag: a reader who clicks the clock meant it, and a later "not this interest" must not take their
// entry away.
type SetSourceReadLater struct {
	Index interfaces.IndexRepository
	Shelf interfaces.ShelfStore
	Clock interfaces.Clock
}

// Execute queues, finishes, or re-opens a source. Queuing an OPEN entry is a no-op; re-queuing a
// finished one puts it at the END of the queue (the entry time is now), because a queue is worked
// front to back.
func (uc SetSourceReadLater) Execute(ctx context.Context, hash string, queued bool) (partialentities.PartialSource, error) {
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

// SetWikiFavorite writes the star on a wiki page. The page file is the truth (the flag lives in its
// frontmatter); there is no index mirror, because the wiki list already reads the files.
type SetWikiFavorite struct {
	Wiki  interfaces.WikiStore
	Clock interfaces.Clock
}

// Execute sets (or clears) the page's star.
func (uc SetWikiFavorite) Execute(ctx context.Context, id ulid.ULID, on bool) (entities.WikiPage, error) {
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
