package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// ListFindings lists stored lint findings.
type ListFindings struct {
	Lint interfaces.LintRepository
}

// Execute returns findings, optionally filtered by kind.
func (uc ListFindings) Execute(ctx context.Context, kind string, limit, offset int) ([]partialentities.PartialFinding, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	findings, err := uc.Lint.ListFindings(ctx, kind, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, partialentities.PartialFinding{
			ID: f.ID.String(), RunID: f.RunID.String(), Layer: string(f.Layer), Kind: f.Kind,
			Severity: string(f.Severity), SubjectKind: string(f.SubjectKind), SubjectID: f.SubjectID, Message: f.Message,
		})
	}
	return out, nil
}
