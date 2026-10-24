package usecases

import (
	"context"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// StartRun creates a running agent run for the given operation and returns its id,
// so the HTTP layer can answer 202 before the agent works.
type StartRun struct {
	Runs  interfaces.RunRepository
	Clock interfaces.Clock
}

// Execute creates the run row. step names which pipeline step the run executes (capture, outline,
// distill, compile); the op stays the class of work.
func (uc StartRun) Execute(ctx context.Context, op entities.RunOp, model string, step entities.Step, target string) (ulid.ULID, error) {
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
	if err := uc.Runs.CreateRun(ctx, run); err != nil {
		return ulid.ULID{}, err
	}
	return id, nil
}

// Abandon closes a run that never started: the run row was created, but the work could not be
// handed to a worker (a full queue). Without this the row would stay `running` forever, and the
// reader would see a pass that neither runs nor ends.
func (uc StartRun) Abandon(ctx context.Context, id ulid.ULID, reason string) error {
	run := entities.Run{
		ID: id, Op: entities.RunPipeline, Status: entities.RunAborted,
		Error: reason, StartedAt: uc.Clock.Now(), EndedAt: uc.Clock.Now(),
	}
	return uc.Runs.FinishRun(ctx, run)
}

// AbandonLeftovers closes every run a PREVIOUS process left "running". It is the first thing a start
// does with the audit: nothing is behind those rows any more, and a history that keeps saying "running"
// is a history that lies about what the app is doing.
func (uc StartRun) AbandonLeftovers(ctx context.Context, reason string) (int64, error) {
	if uc.Runs == nil {
		return 0, nil
	}
	return uc.Runs.AbandonRunningRuns(ctx, reason, uc.Clock.Now())
}

// ExecuteQuery creates a QUERY run. The question is part of it from the start — it is what the run
// answers, the Ask view shows it beside the answer, and filing the answer into the cluster layer files the
// question with it — so it is written when the run is created, not only when it succeeds.
func (uc StartRun) ExecuteQuery(ctx context.Context, model, question string) (ulid.ULID, error) {
	id := ulid.Make()
	run := entities.Run{
		ID:        id,
		Op:        entities.RunQuery,
		Model:     model,
		Question:  strings.TrimSpace(question),
		Status:    entities.RunRunning,
		StartedAt: uc.Clock.Now(),
	}
	if err := uc.Runs.CreateRun(ctx, run); err != nil {
		return ulid.ULID{}, err
	}
	return id, nil
}
