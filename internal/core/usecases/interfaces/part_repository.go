package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// PartRepository records how far the distilling of each PART of a source has come. It exists
// because "this part has atoms" does not mean "this part is finished": a run can stop mid-part, and
// a resumed run must derive such a part again instead of skipping it.
type PartRepository interface {
	SetPartState(ctx context.Context, sourceID ulid.ULID, s entities.PartState) error
	PartStates(ctx context.Context, sourceID ulid.ULID) (map[string]entities.PartState, error)
	ClearParts(ctx context.Context, sourceID ulid.ULID) error
	// PruneWithoutText removes records for nodes that are gone or carry no text. A part is a
	// text-bearing node, so such a record is a stale leftover, not progress.
	PruneWithoutText(ctx context.Context) (int64, error)
}
