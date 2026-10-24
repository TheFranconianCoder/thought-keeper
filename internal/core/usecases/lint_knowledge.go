package usecases

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// LintKnowledge runs the programmatic health checks over the claims. The semantic (LLM) pass is a
// separate concern.
type LintKnowledge struct {
	Lint  interfaces.LintRepository
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

// Execute replaces the programmatic findings and finishes the run row.
func (uc LintKnowledge) Execute(ctx context.Context, runID ulid.ULID) (LintResult, error) {
	if err := uc.Lint.DeleteFindingsByLayer(ctx, entities.FindingProgrammatic); err != nil {
		return LintResult{}, err
	}
	findings := ProgrammaticFindings(ctx, uc.Lint, uc.Atoms, runID, uc.Clock.Now())
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
	if err := uc.Lint.AddFindings(ctx, findings); err != nil && uc.Log != nil {
		uc.Log.Warn("lint: write findings", "err", err)
	}

	now := uc.Clock.Now()
	run := entities.Run{
		ID: runID, Op: entities.RunLint, Status: entities.RunSucceeded,
		Answer: fmt.Sprintf("%d findings · %d errors · %d warnings · %d info",
			result.Findings, result.Errors, result.Warnings, result.Info),
		EndedAt: now,
	}
	_ = uc.Runs.FinishRun(ctx, run)
	return result, nil
}
