package usecases

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Lint is the use case of the health checks: the findings a pass wrote, and the pass itself.
type Lint struct {
	Store interfaces.LintRepository
	Atoms interfaces.AtomRepository
	Runs  interfaces.RunRepository
	Clock interfaces.Clock
	Log   *slog.Logger
}

// LintResult summarizes a lint pass.
type LintResult struct {
	Findings int
	Errors   int
	Warnings int
	Info     int
}

// Check runs the programmatic pass and replaces the findings it owns.
func (uc Lint) Check(ctx context.Context, runID ulid.ULID) (LintResult, error) {
	if err := uc.Store.DeleteFindingsByLayer(ctx, entities.FindingProgrammatic); err != nil {
		return LintResult{}, err
	}
	findings := ProgrammaticFindings(ctx, uc.Store, uc.Atoms, runID, uc.Clock.Now())
	result := LintResult{Findings: len(findings)}
	for _, f := range findings {
		switch f.Severity {
		case entities.SeverityError:
			result.Errors++
		case entities.SeverityWarning:
			result.Warnings++
		default:
			result.Info++
		}
	}
	if err := uc.Store.AddFindings(ctx, findings); err != nil && uc.Log != nil {
		uc.Log.Warn("lint: write findings", "err", err)
	}

	now := uc.Clock.Now()
	run := entities.Run{
		ID: runID, Op: entities.RunLint, Status: entities.RunSucceeded,
		Answer: fmt.Sprintf("%d findings · %d errors · %d warnings · %d info",
			result.Findings, result.Errors, result.Warnings, result.Info),
		EndedAt: now,
	}
	_ = uc.Runs.FinishRun(ctx, run) //nolint:errcheck // the run row is an audit trail; the work already ended
	return result, nil
}
