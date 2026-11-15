package interfaces

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ReaderStore reads and writes the vault's two seen watermarks. It is a RECORD, not a cache:
// reindex leaves it alone, because a reader's view of what is new cannot be derived from the
// sources.
type ReaderStore interface {
	// Read returns both watermarks. A zero field means that scope has not been acknowledged yet;
	// a missing record is a vault no client has opened, not an error.
	Read(ctx context.Context) (entities.ReaderState, error)
	// Write stores both watermarks atomically.
	Write(ctx context.Context, state entities.ReaderState) error
}

// ReaderIndex answers the newest activity in the index: the last real content change of a page,
// otherwise the arrival. The zero time means an empty vault.
type ReaderIndex interface {
	MaxSourceActivityAt(ctx context.Context) (time.Time, error)
}
