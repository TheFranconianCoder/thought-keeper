package interfaces

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ReaderStore reads and writes the vault's seen watermark. It is a RECORD, not a cache: reindex
// leaves it alone, because a reader's view of what is new cannot be derived from the sources.
type ReaderStore interface {
	// Read returns the watermark. A zero value means the vault has not been acknowledged yet; a
	// missing record is a vault no client has opened, not an error.
	Read(ctx context.Context) (entities.ReaderState, error)
	// WriteSeen stores the acknowledged instant atomically.
	WriteSeen(ctx context.Context, at time.Time) error
}

// ReaderIndex answers the newest activity in the index: the last real content change of a page,
// otherwise the arrival. The zero time means an empty vault.
type ReaderIndex interface {
	MaxSourceActivityAt(ctx context.Context) (time.Time, error)
}
