package interfaces

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// RunRepository is the append-only audit for agent runs.
type RunRepository interface {
	CreateRun(ctx context.Context, r entities.Run) error
	FinishRun(ctx context.Context, r entities.Run) error
	AddStep(ctx context.Context, s entities.RunStep) error
	GetRun(ctx context.Context, id ulid.ULID) (entities.Run, error)
	ListSteps(ctx context.Context, id ulid.ULID) ([]entities.RunStep, error)
	// AbandonRunningRuns closes every run a previous process left "running" (nothing is behind it)
	// and reports how many rows it closed.
	AbandonRunningRuns(ctx context.Context, reason string, at time.Time) (int64, error)
}
