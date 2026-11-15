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
// and updated marks of its rows. Visiting one lens acknowledges only its own scope, so the marks
// stay until Sources is actually opened. Each watermark is ONE per vault, never per client: the
// first client that becomes active on a lens acknowledges that scope for every client, so a source
// the phone has seen is not new on the desktop. A client's active phase only decides WHEN to
// acknowledge; no identity and no per-client state are stored.
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

// Activate acknowledges everything that happened in the vault up to now for ONE scope and returns
// the baseline that scope should mark from.
//
// The watermark only moves FORWARD, and only when there is something later: an activation that
// finds no later arrival and no later content change writes nothing and reports Changed=false, so
// no other client is reloaded for nothing. The first activation of a scope initializes it to "now":
// the whole vault is the baseline, never "all of it is new" — a reader who opens a fresh vault does
// not want every source flagged, and a first Sources visit must not clear a batch Start never saw.
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

	if previous.IsZero() {
		seen := now
		if newest.After(seen) {
			seen = newest
		}
		uc.setSeen(&state, scope, seen)
		if err := uc.Store.Write(ctx, state); err != nil {
			return entities.ReaderActivation{}, err
		}
		return entities.ReaderActivation{Scope: scope, SeenAt: seen, First: true, Changed: true}, nil
	}

	act := entities.ReaderActivation{Scope: scope, Since: previous, SeenAt: previous}
	if newest.After(previous) {
		uc.setSeen(&state, scope, newest)
		if err := uc.Store.Write(ctx, state); err != nil {
			return entities.ReaderActivation{}, err
		}
		act.SeenAt = newest
		act.Changed = true
	}
	return act, nil
}

// setSeen writes one scope's watermark and leaves the other untouched.
func (uc *Reader) setSeen(state *entities.ReaderState, scope entities.ReaderScope, at time.Time) {
	if scope == entities.ReaderScopeSources {
		state.SourcesSeenAt = at
		return
	}
	state.StartSeenAt = at
}
