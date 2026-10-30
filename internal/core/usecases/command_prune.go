package usecases

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// PruneCommands enforces the assistant's retention: a command is a UI state from the last few days,
// not a record of the vault, and "not endless" is a decision, not an accident.
//
// The removal goes through the store's trash move, like every other deletion, so a mistaken pruning
// is recoverable. A retention of zero disables the pass entirely.
type PruneCommands struct {
	Commands  interfaces.CommandStore
	Retention time.Duration
	Clock     interfaces.Clock
}

// Execute moves every command older than the retention window to the trash.
func (uc PruneCommands) Execute(ctx context.Context) (int, error) {
	if uc.Retention <= 0 || uc.Commands == nil {
		return 0, nil
	}
	return uc.Commands.Prune(ctx, uc.Clock.Now().Add(-uc.Retention))
}
