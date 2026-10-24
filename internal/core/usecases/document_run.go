package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// DocumentRun runs the DOCUMENT steps of one source: outline → distill.
//
// It is deliberately NOT the whole pipeline. The four steps are strictly separated:
//
//	capture → outline → distill → compile
//
// capture happens at intake, this run does outline and distill, and COMPILE is a separate step of
// its own (the cluster step) that runs over the whole vault and only while no document step runs.
// Keeping them apart is what makes the model honest: a source's atoms are derived from that source
// alone, and the cluster layer is the only place where sources meet — so a run of this one ends with the
// claims and nothing else, and the cluster step picks them up when the documents are quiet.
//
// It also keeps the run bounded and resumable: the parts this run did not reach stay open, and
// the next run of the same source continues exactly there.
type DocumentRun struct {
	// Distill is the distill step.
	Distill DistillSource
	// Reset wipes everything derived (tree, claims, pages) for an explicit "start over". The reset
	// run only CLEARS; the global pump then rebuilds capture → outline → distill without jumping
	// ahead of another source. BuildTree and Capture stay on the struct for callers that build a
	// DocumentRun, but a reset never runs them inside this run.
	Reset ResetSource
	// BuildTree / Capture are retained for wiring compatibility; the reset path no longer runs a
	// later step inside the reset run.
	BuildTree BuildTree
	Capture   CaptureSource
	Runs      interfaces.RunRepository
	// SourceLog records the whole run where the source lives.
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	Model     string
	Log       *slog.Logger
}

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
func (uc DocumentRun) log(ctx context.Context, runID ulid.ULID, hash, level, phase, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), RunID: runID.String(), Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

// Execute runs the document steps of one source and finishes the run row. reset=false CONTINUES the
// derivation: a part that already has atoms is skipped, so a long document converges over
// several runs and a retry after an abort keeps its progress. reset=true is the explicit "replace
// the derivation" and cleans first.
func (uc DocumentRun) Execute(ctx context.Context, runID ulid.ULID, hash string, reset bool) (DocumentRunResult, error) {
	rec := NewStepRecorder(ctx, uc.Runs, runID, uc.Log)
	run := entities.Run{
		ID: runID, Op: entities.RunPipeline, Step: entities.StepDistill, Model: uc.Model,
		TargetID: hash, StartedAt: uc.Clock.Now(),
	}
	if uc.Log != nil {
		uc.Log.Info("document run start", "hash", hash, "run", runID.String(), "model", uc.Model)
	}
	uc.log(ctx, runID, hash, "info", "distill", "document run start", map[string]any{
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
		r, rErr := uc.Reset.Execute(ctx, hash)
		cleaned = CleanResult{Atoms: r.Atoms}
		if rErr != nil {
			return DocumentRunResult{}, uc.fail(ctx, run, "reset: "+rErr.Error())
		}
		// Capture is step 1, so it may run here — it is earlier than the barrier, not later. The
		// outline and distill are NOT run here: the pump rebuilds them when every source is ready.
		if uc.Capture.Index != nil && needsCapture(ctx, uc.Capture.Index, hash) {
			if _, cErr := uc.Capture.Execute(ctx, hash); cErr != nil {
				return DocumentRunResult{Cleaned: cleaned}, uc.fail(ctx, run, "capture: "+cErr.Error())
			}
		}
		run.Answer = "reset: the source's derivation was cleared; the pipeline will rebuild it"
		_ = uc.finish(ctx, run, entities.RunSucceeded, "")
		return DocumentRunResult{Cleaned: cleaned, Summary: "reset"}, nil
	}

	a, aErr := uc.Distill.Distill(ctx, hash, rec)
	run.Iterations = a.Iterations
	run.TotalTokens = a.TotalTokens
	if aErr != nil {
		return DocumentRunResult{Atoms: a.Atoms, Cleaned: cleaned, Iterations: run.Iterations, TotalTokens: run.TotalTokens},
			uc.fail(ctx, run, aErr.Error())
	}

	pending := a.PartsLeft > 0
	uc.log(ctx, runID, hash, "info", "distill", "document run finished", map[string]any{
		"atoms": a.Atoms, "written": a.Written, "parts_done": a.PartsDone, "parts_total": a.PartsTotal,
		"parts_left": a.PartsLeft, "tokens": run.TotalTokens,
		"dur": uc.Clock.Now().Sub(startedAt).Round(time.Second).String(),
	})
	run.Answer = a.Summary
	if pending {
		run.Answer = fmt.Sprintf("partial: %d/%d parts · %d atoms (%d this run) · %d parts left",
			a.PartsDone, a.PartsTotal, a.Atoms, a.Written, a.PartsLeft)
	}
	_ = uc.finish(ctx, run, entities.RunSucceeded, "")
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
func (uc DocumentRun) fail(ctx context.Context, run entities.Run, errMsg string) error {
	if recErr := uc.finish(ctx, run, entities.RunFailed, errMsg); recErr != nil {
		return fmt.Errorf("%s (recording the failure: %w)", errMsg, recErr)
	}
	return errors.New(errMsg)
}

func (uc DocumentRun) finish(ctx context.Context, run entities.Run, status entities.RunStatus, errMsg string) error {
	run.Status = status
	run.Error = errMsg
	run.EndedAt = uc.Clock.Now()
	if status == entities.RunFailed {
		uc.log(ctx, run.ID, run.TargetID, "error", "distill", "document run failed", map[string]any{"err": errMsg})
	}
	return uc.Runs.FinishRun(ctx, run)
}
