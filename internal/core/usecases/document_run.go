package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// needsCapture reports whether a source's kind needs step 1's extraction. A source that cannot be
// read (a missing row) needs nothing: the run will fail honestly a moment later.
func needsCapture(ctx context.Context, index interfaces.IndexRepository, hash string) bool {
	src, err := index.GetSourceByHash(ctx, hash)
	if err != nil {
		return false
	}
	return src.NeedsCapture()
}

// DocumentRunResult summarizes a document run.
type DocumentRunResult struct {
	// Atoms is the source's TOTAL after the run; Written is what THIS run added. Both are reported
	// because they answer different questions, and a run that says "187 atoms" after writing 65 reads
	// like a swing between runs when it is only growth.
	Atoms   int
	Written int
	// Cleaned is what a reset threw away.
	Cleaned CleanResult
	// Pending is true when the derivation is not finished: parts without a claim are left, so the
	// document run has to run again (it continues).
	Pending     bool
	PartsDone   int
	PartsTotal  int
	PartsLeft   int
	Iterations  int
	TotalTokens int
	Summary     string
}

// log records one source-scoped event (no-op when no log is configured).
func (uc Derivation) logRun(ctx context.Context, runID ulid.ULID, hash, level, phase, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), RunID: runID.String(), Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

// DocumentRunner runs one document through its steps under a run record.
type DocumentRunner func(ctx context.Context, runID ulid.ULID, hash string, reset bool) (DocumentRunResult, error)

// Run runs one document through the steps it is owed.
func (uc Derivation) Run(ctx context.Context, runID ulid.ULID, hash string, reset bool) (DocumentRunResult, error) {
	rec := NewStepRecorder(ctx, uc.Runs, runID, uc.Log)
	run := entities.Run{
		ID: runID, Op: entities.RunPipeline, Step: entities.StepDistill, Model: uc.Model,
		TargetID: hash, StartedAt: uc.Clock.Now(),
	}
	if uc.Log != nil {
		uc.Log.Info("document run start", "hash", hash, "run", runID.String(), "model", uc.Model)
	}
	uc.logRun(ctx, runID, hash, "info", "distill", "document run start", map[string]any{
		"model": uc.Model, "mode": map[bool]string{true: "reset", false: "continue"}[reset],
	})
	startedAt := uc.Clock.Now()

	// A reset replaces the previous derivation (the explicit "start over"). Continuing keeps
	// what was derived: deleting it would make a long document impossible to finish.
	cleaned := CleanResult{}
	if reset {
		// A reset is its own step, not a shortcut through the pipeline: it throws the derivation away
		// and returns. The global pump then brings this source up through capture → outline → distill
		// together with every other source; a reset must not distil ahead of a source that is still
		// missing its outline.
		r, rErr := uc.Reset(ctx, hash)
		cleaned = CleanResult{Atoms: r.Atoms}
		if rErr != nil {
			return DocumentRunResult{}, uc.fail(ctx, run, fmt.Errorf("reset: %w", rErr))
		}
		// Capture is step 1, so it may run here — it is earlier than the barrier, not later. The
		// outline and distill are NOT run here: the pump rebuilds them when every source is ready.
		if uc.Index != nil && needsCapture(ctx, uc.Index, hash) {
			if _, cErr := uc.Capture(ctx, hash); cErr != nil {
				return DocumentRunResult{Cleaned: cleaned}, uc.fail(ctx, run, fmt.Errorf("capture: %w", cErr))
			}
		}
		run.Answer = "reset: the source's derivation was cleared; the pipeline will rebuild it"
		_ = uc.finish(ctx, run, entities.RunSucceeded, "") //nolint:errcheck // the run row is an audit trail; the work already ended
		return DocumentRunResult{Cleaned: cleaned, Summary: "reset"}, nil
	}

	a, aErr := uc.Distill(ctx, hash, rec)
	run.Iterations = a.Iterations
	run.TotalTokens = a.TotalTokens
	if aErr != nil {
		return DocumentRunResult{Atoms: a.Atoms, Cleaned: cleaned, Iterations: run.Iterations, TotalTokens: run.TotalTokens},
			uc.fail(ctx, run, aErr)
	}

	pending := a.PartsLeft > 0
	uc.logRun(ctx, runID, hash, "info", "distill", "document run finished", map[string]any{
		"atoms": a.Atoms, "written": a.Written, "parts_done": a.PartsDone, "parts_total": a.PartsTotal,
		"parts_left": a.PartsLeft, "tokens": run.TotalTokens,
		"dur": uc.Clock.Now().Sub(startedAt).Round(time.Second).String(),
	})
	run.Answer = a.Summary
	if pending {
		run.Answer = fmt.Sprintf("partial: %d/%d parts · %d atoms (%d this run) · %d parts left",
			a.PartsDone, a.PartsTotal, a.Atoms, a.Written, a.PartsLeft)
	}
	_ = uc.finish(ctx, run, entities.RunSucceeded, "") //nolint:errcheck // the run row is an audit trail; the work already ended
	return DocumentRunResult{
		Atoms: a.Atoms, Written: a.Written, Cleaned: cleaned,
		Pending: pending, PartsDone: a.PartsDone, PartsTotal: a.PartsTotal,
		PartsLeft: a.PartsLeft, Iterations: run.Iterations, TotalTokens: run.TotalTokens,
		Summary: a.Summary,
	}, nil
}

// fail records the failure and RETURNS it. Recording it in the run row is not enough: the
// caller must learn that this run failed, or the UI reports a dead run as succeeded and the
// user only finds out by reading the log. `finish` returns the error of the *recording*, which
// is almost always nil — hence this wrapper.
func (uc Derivation) fail(ctx context.Context, run entities.Run, cause error) error {
	if recErr := uc.finish(ctx, run, entities.RunFailed, cause.Error()); recErr != nil {
		// Both stay in the chain: the caller acts on the CAUSE (a stale outline is rebuilt), and the
		// recording error is still there to be read.
		return fmt.Errorf("%w (recording the failure: %w)", cause, recErr)
	}
	return cause
}

func (uc Derivation) finish(ctx context.Context, run entities.Run, status entities.RunStatus, errMsg string) error {
	run.Status = status
	run.Error = errMsg
	run.EndedAt = uc.Clock.Now()
	if status == entities.RunFailed {
		uc.logRun(ctx, run.ID, run.TargetID, "error", "distill", "document run failed", map[string]any{"err": errMsg})
	}
	return uc.Runs.FinishRun(ctx, run)
}
