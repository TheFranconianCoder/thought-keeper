package usecases

import (
	"context"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Reader is the use case of the shared "new" watermark.
//
// A source is NEW when it arrived after SeenAt, and UPDATED when its page content changed after it.
// The watermark is ONE per vault, never per client: the first client that becomes active while
// Start or Sources is visible acknowledges the batch, so a source the phone has seen is not new on
// the desktop. A client's active phase only decides WHEN to acknowledge; no identity and no
// per-client state are stored.
type Reader struct {
	Store interfaces.ReaderStore
	Index interfaces.ReaderIndex
	Clock interfaces.Clock

	// mu serializes the read-modify-write: two clients opening at the same moment must not both
	// claim the same batch.
	mu sync.Mutex
}

// State returns the watermark as it is, without acknowledging anything.
func (uc *Reader) State(ctx context.Context) (entities.ReaderState, error) {
	seen, _, err := uc.Store.Read(ctx)
	if err != nil {
		return entities.ReaderState{}, err
	}
	return entities.ReaderState{SeenAt: seen}, nil
}

// Activate acknowledges everything that happened in the vault up to now and returns the baseline
// the caller should mark from.
//
// The watermark only moves FORWARD, and only when there is something later: an activation that
// finds no later arrival and no later content change writes nothing and reports Changed=false, so
// no other client is reloaded for nothing. The first activation initializes the record to "now":
// the whole vault is the baseline, never "all of it is new" — a reader who opens a fresh vault does
// not want every source flagged.
func (uc *Reader) Activate(ctx context.Context) (entities.ReaderActivation, error) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	previous, exists, err := uc.Store.Read(ctx)
	if err != nil {
		return entities.ReaderActivation{}, err
	}
	newest, err := uc.Index.MaxSourceActivityAt(ctx)
	if err != nil {
		return entities.ReaderActivation{}, err
	}
	// The index stores the activity instants in milliseconds; keep the baseline on the same clock
	// so a later comparison is never off by a fraction the store cannot represent.
	now := uc.Clock.Now().UTC().Truncate(time.Millisecond)

	if !exists || previous.IsZero() {
		seen := now
		if newest.After(seen) {
			seen = newest
		}
		if err := uc.Store.Write(ctx, seen); err != nil {
			return entities.ReaderActivation{}, err
		}
		return entities.ReaderActivation{SeenAt: seen, First: true, Changed: true}, nil
	}

	act := entities.ReaderActivation{Since: previous, SeenAt: previous}
	if newest.After(previous) {
		if err := uc.Store.Write(ctx, newest); err != nil {
			return entities.ReaderActivation{}, err
		}
		act.SeenAt = newest
		act.Changed = true
	}
	return act, nil
}
