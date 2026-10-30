package interfaces

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// CommandStore persists the assistant's commands in the vault's commands/ area. A command is a
// short-lived record: it is written once, read back by the lens it addressed, and pruned after the
// configured retention. There is no update — a new request is a new command.
type CommandStore interface {
	Write(ctx context.Context, c entities.Command) error
	Get(ctx context.Context, id ulid.ULID) (entities.Command, error)
	// List returns the newest commands first.
	List(ctx context.Context, limit, offset int) ([]entities.Command, error)
	Delete(ctx context.Context, id ulid.ULID) error
	// Prune moves every command created before the cutoff to the vault trash and returns how many
	// it removed. A retention of zero is handled by the caller, not here.
	Prune(ctx context.Context, before time.Time) (int, error)
}
