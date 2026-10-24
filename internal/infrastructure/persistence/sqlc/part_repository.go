package sqlc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc/dal"
)

// PartRepository records how far the distilling of a source's parts has come.
type PartRepository struct {
	db *sql.DB
	q  *dal.Queries
}

var _ interfaces.PartRepository = (*PartRepository)(nil)

// NewPartRepository wraps a database handle.
func NewPartRepository(db *sql.DB) *PartRepository {
	return &PartRepository{db: db, q: dal.New(db)}
}

// SetPartState records the outcome of distilling one part.
func (r *PartRepository) SetPartState(ctx context.Context, sourceID ulid.ULID, s entities.PartState) error {
	var runID []byte
	if s.RunID != (ulid.ULID{}) {
		runID = s.RunID[:]
	}
	if err := r.q.UpsertPartState(ctx, dal.UpsertPartStateParams{
		SourceID:  sourceID[:],
		NodeID:    s.NodeID,
		State:     string(s.State),
		Reason:    s.Reason,
		Atoms:     int64(s.Atoms),
		RunID:     runID,
		UpdatedAt: s.UpdatedAt.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("set part state: %w", err)
	}
	return nil
}

// PartStates returns what is known about every part of a source.
func (r *PartRepository) PartStates(ctx context.Context, sourceID ulid.ULID) (map[string]entities.PartState, error) {
	rows, err := r.q.ListPartStates(ctx, sourceID[:])
	if err != nil {
		return nil, fmt.Errorf("list part states: %w", err)
	}
	out := make(map[string]entities.PartState, len(rows))
	for _, row := range rows {
		out[row.NodeID] = entities.PartState{
			NodeID:    row.NodeID,
			State:     entities.PartStateValue(row.State),
			Reason:    row.Reason,
			Atoms:     int(row.Atoms),
			UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		}
	}
	return out, nil
}

// PruneWithoutText removes part records for nodes that no longer exist or carry no text.
func (r *PartRepository) PruneWithoutText(ctx context.Context) (int64, error) {
	n, err := r.q.DeletePartsWithoutText(ctx)
	if err != nil {
		return 0, fmt.Errorf("prune part states: %w", err)
	}
	return n, nil
}

// ClearParts forgets every part record of a source (a full re-derivation starts over).
func (r *PartRepository) ClearParts(ctx context.Context, sourceID ulid.ULID) error {
	if err := r.q.DeletePartStates(ctx, sourceID[:]); err != nil {
		return fmt.Errorf("clear part states: %w", err)
	}
	return nil
}
