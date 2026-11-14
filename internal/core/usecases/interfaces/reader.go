package interfaces

import (
	"context"
	"time"
)

// ReaderStore reads and writes the vault's seen watermark. It is a RECORD, not a cache: reindex
// leaves it alone, because a reader's view of what is new cannot be derived from the sources.
type ReaderStore interface {
	// Read returns the watermark and whether the record exists. A missing record is a vault no
	// client has opened yet, not an error.
	Read(ctx context.Context) (time.Time, bool, error)
	// Write stores the watermark atomically.
	Write(ctx context.Context, seenAt time.Time) error
}

// ReaderIndex answers the newest activity in the index: the last real content change of a page,
// otherwise the arrival. The zero time means an empty vault.
type ReaderIndex interface {
	MaxSourceActivityAt(ctx context.Context) (time.Time, error)
}
