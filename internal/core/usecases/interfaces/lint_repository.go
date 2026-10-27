package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// LintRepository stores lint findings (append-only audit) and the analysis reads the programmatic
// lint needs. The page-layer reads (ghost links, orphans, untagged) are gone with the page layer;
// what remains is atom-level evidence.
type LintRepository interface {
	// AddFindings inserts a batch in one transaction.
	AddFindings(ctx context.Context, findings []entities.LintFinding) error
	DeleteFindingsByLayer(ctx context.Context, layer entities.FindingLayer) error
	ListFindings(ctx context.Context, kind string, limit, offset int) ([]entities.LintFinding, error)

	// Analysis reads.
	AtomsWithoutSource(ctx context.Context, limit int) ([]entities.Atom, error)
}
