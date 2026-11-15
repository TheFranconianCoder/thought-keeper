package usecases

import (
	"context"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Reader is the use case of the vault's two seen watermarks.
//
// Start's watermark drives the statistic the front door shows; Sources' watermark drives the new
// and updated marks of its rows. A Start visit settles the front door alone, so the marks stay
// until Sources is actually opened; a Sources visit settles both, because the list answers the same
// batch. Each watermark is ONE per vault, never per client: the first client that becomes active on
// a lens acknowledges that scope for every client, so a source the phone has seen is not new on the
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

// State returns both watermarks as they are, without acknowledging anything.
func (uc *Reader) State(ctx context.Context) (entities.ReaderState, error) {
	return uc.Store.Read(ctx)
}

// Activate acknowledges everything that happened in the vault up to now, from the lens the reader
// opened, and returns the baseline that lens should mark from.
//
// The watermarks only move FORWARD, and only when there is something later: an activation that
// finds no later arrival and no later content change writes nothing and settles nothing, so no
// other client is reloaded for nothing. The first activation of a scope initializes it to "now":
// the whole vault is the baseline, never "all of it is new" — a reader who opens a fresh vault does
// not want every source flagged.
//
// A Start visit settles Start's watermark alone: the front door is a summary, and the Sources marks
// must survive until the reader actually opens the list. A Sources visit settles BOTH: the list
// answers the same batch the front door summarizes, so the statistic is seen as well. A scope the
// visit did not settle keeps its stored instant — a pre-split record's shared value included.
func (uc *Reader) Activate(ctx context.Context, scope entities.ReaderScope) (entities.ReaderActivation, error) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	state, err := uc.Store.Read(ctx)
	if err != nil {
		return entities.ReaderActivation{}, err
	}
	previous := state.StartSeenAt
	if scope == entities.ReaderScopeSources {
		previous = state.SourcesSeenAt
	}
	newest, err := uc.Index.MaxSourceActivityAt(ctx)
	if err != nil {
		return entities.ReaderActivation{}, err
	}
	// The index stores the activity instants in milliseconds; keep the baseline on the same clock
	// so a later comparison is never off by a fraction the store cannot represent.
	now := uc.Clock.Now().UTC().Truncate(time.Millisecond)

	// A scope's first visit starts at "now"; a later one moves to the newest activity, and only when
	// that is actually later.
	target := newest
	if previous.IsZero() {
		target = now
		if newest.After(target) {
			target = newest
		}
	}

	var settled []entities.ReaderWatermark
	if previous.IsZero() || target.After(previous) {
		settled = append(settled, entities.ReaderWatermark{Scope: scope, SeenAt: target})
	}
	// Opening Sources settles the front door's statistic too: the reader has seen the list that
	// answers the same batch. It never moves backwards.
	if scope == entities.ReaderScopeSources && state.StartSeenAt.Before(target) {
		settled = append(settled, entities.ReaderWatermark{
			Scope: entities.ReaderScopeStart, SeenAt: target,
		})
	}
	if len(settled) > 0 {
		if err := uc.Store.WriteScopes(ctx, settled); err != nil {
			return entities.ReaderActivation{}, err
		}
	}

	act := entities.ReaderActivation{Scope: scope, Settled: settled}
	if previous.IsZero() {
		act.First = true
		act.SeenAt = target
		return act, nil
	}
	act.Since = previous
	act.SeenAt = previous
	if target.After(previous) {
		act.SeenAt = target
	}
	return act, nil
}
