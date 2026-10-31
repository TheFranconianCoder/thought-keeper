package usecases

import (
	"context"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Runs is the use case of the run records: every pass of the pipeline, a query, a lint or a tag run is
// one row, created before the work starts and closed by whoever ran it.
type Runs struct {
	Store interfaces.RunRepository
	Clock interfaces.Clock
}

// Start creates the run row. step names which pipeline step the run executes (capture, outline,
// distill, compile); the op stays the class of work.
func (uc Runs) Start(ctx context.Context, op entities.RunOp, model string, step entities.Step, target string) (ulid.ULID, error) {
	id := ulid.Make()
	run := entities.Run{
		ID:        id,
		Op:        op,
		Step:      step,
		Model:     model,
		TargetID:  target,
		Status:    entities.RunRunning,
		StartedAt: uc.Clock.Now(),
	}
	if err := uc.Store.CreateRun(ctx, run); err != nil {
		return ulid.ULID{}, err
	}
	return id, nil
}

// Abandon closes a run that never started: the run row was created, but the work could not be
// handed to a worker (a full queue). Without this the row would stay `running` forever, and the
// reader would see a pass that neither runs nor ends.
func (uc Runs) Abandon(ctx context.Context, id ulid.ULID, reason string) error {
	run := entities.Run{
		ID: id, Op: entities.RunPipeline, Status: entities.RunAborted,
		Error: reason, StartedAt: uc.Clock.Now(), EndedAt: uc.Clock.Now(),
	}
	return uc.Store.FinishRun(ctx, run)
}

// AbandonLeftovers closes every run a PREVIOUS process left "running". It is the first thing a start
// does with the audit: nothing is behind those rows any more, and a history that keeps saying "running"
// is a history that lies about what the app is doing.
func (uc Runs) AbandonLeftovers(ctx context.Context, reason string) (int64, error) {
	if uc.Store == nil {
		return 0, nil
	}
	return uc.Store.AbandonRunningRuns(ctx, reason, uc.Clock.Now())
}

// StartQuery creates the run row for one question. A non-empty source is the run's target: the
// question is scoped to that source and may not read any other. The question is part of the run from
// the start — it is what the run answers, the Ask view shows it beside the answer, and filing the
// answer into the cluster layer files the question with it — so it is written when the run is
// created, not only when it succeeds.
func (uc Runs) StartQuery(ctx context.Context, model, question, source string) (ulid.ULID, error) {
	id := ulid.Make()
	run := entities.Run{
		ID:        id,
		Op:        entities.RunQuery,
		Model:     model,
		TargetID:  source,
		Question:  strings.TrimSpace(question),
		Status:    entities.RunRunning,
		StartedAt: uc.Clock.Now(),
	}
	if err := uc.Store.CreateRun(ctx, run); err != nil {
		return ulid.ULID{}, err
	}
	return id, nil
}
