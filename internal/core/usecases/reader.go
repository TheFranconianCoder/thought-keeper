package usecases

import (
	"context"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Reader is the use case of the vault's one seen watermark: the instant up to which arrivals and
// page content changes have been read.
//
// The Sources list owns the acknowledgment; the Start lens only summarizes the batch the list still
// marks, so both views answer the same instant and can never disagree. Opening Start therefore
// settles nothing: the front door is a summary, and the marks belong to the list the reader has not
// opened yet. The watermark is ONE per vault, never per client: the first client that opens the
// list acknowledges the batch for every client, so a source the phone has seen is not new on the
// desktop. A client's active phase only decides WHEN to acknowledge; no identity and no per-client
// state are stored.
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
	return uc.Store.Read(ctx)
}

// Activate answers the lens that just became active with the baseline it marks from, and lets the
// list acknowledge everything that happened in the vault up to now.
//
// The watermark only moves FORWARD, and only when there is something later: an acknowledgment that
// finds no later arrival and no later content change writes nothing, so no other client is
// reloaded for nothing. The first acknowledgment initializes it to "now": the whole vault is the
// baseline, never "all of it is new" — a reader who opens a fresh vault does not want every source
// flagged.
func (uc *Reader) Activate(ctx context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	state, err := uc.Store.Read(ctx)
	if err != nil {
		return entities.ReaderActivation{}, err
	}
	previous := state.SourcesSeenAt
	// The answer is the batch as it stands: its baseline is the watermark itself. A lens that
	// settles nothing (the front door) gets exactly that, so its statistic marks from the same
	// instant the list's rows do.
	act := entities.ReaderActivation{
		Scope:  scope,
		Since:  previous,
		SeenAt: previous,
		First:  previous.IsZero(),
	}
	if scope != entities.ReaderScopeSources {
		return act, nil
	}

	newest, err := uc.Index.MaxSourceActivityAt(ctx)
	if err != nil {
		return entities.ReaderActivation{}, err
	}
	// The index stores the activity instants in milliseconds; keep the baseline on the same clock
	// so a later comparison is never off by a fraction the store cannot represent.
	now := uc.Clock.Now().UTC().Truncate(time.Millisecond)

	target := newest
	if previous.IsZero() {
		target = now
		if newest.After(target) {
			target = newest
		}
	}
	if !previous.IsZero() && !target.After(previous) {
		return act, nil
	}
	if err := uc.Store.WriteSeen(ctx, target); err != nil {
		return entities.ReaderActivation{}, err
	}
	act.SeenAt = target
	return act, nil
}
