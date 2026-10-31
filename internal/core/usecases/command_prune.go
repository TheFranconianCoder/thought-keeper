package usecases

import (
	"context"
)

// PruneCommands enforces the assistant's retention: a command is a UI state from the last few days,
// not a record of the vault, and "not endless" is a decision, not an accident.
//
// The removal goes through the store's trash move, like every other deletion, so a mistaken pruning
// is recoverable. A retention of zero disables the pass entirely.

// Prune moves every command older than the retention window to the trash.
func (uc Commands) Prune(ctx context.Context) (int, error) {
	if uc.Retention <= 0 || uc.Store == nil {
		return 0, nil
	}
	return uc.Store.Prune(ctx, uc.Clock.Now().Add(-uc.Retention))
}
