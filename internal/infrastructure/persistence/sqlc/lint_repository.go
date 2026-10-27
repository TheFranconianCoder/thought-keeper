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

// LintRepository implements interfaces.LintRepository.
type LintRepository struct {
	db *sql.DB
	q  *dal.Queries
}

var _ interfaces.LintRepository = (*LintRepository)(nil)

// NewLintRepository wraps a database handle.
func NewLintRepository(db *sql.DB) *LintRepository {
	return &LintRepository{db: db, q: dal.New(db)}
}

// AddFindings inserts a batch of findings in a single transaction. One statement per
// finding (autocommit) made lint crawl on a large vault; a batched write does not.
func (r *LintRepository) AddFindings(ctx context.Context, findings []entities.LintFinding) error {
	if len(findings) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin lint tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := r.q.WithTx(tx)
	for _, f := range findings {
		var runID []byte
		if f.RunID != (ulid.ULID{}) {
			runID = f.RunID[:]
		}
		if err := q.InsertLintFinding(ctx, dal.InsertLintFindingParams{
			ID:          f.ID[:],
			RunID:       runID,
			Layer:       string(f.Layer),
			Kind:        f.Kind,
			Severity:    string(f.Severity),
			SubjectKind: string(f.SubjectKind),
			SubjectID:   subjectBytes(f.SubjectID),
			Message:     f.Message,
			CreatedAt:   f.CreatedAt.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("insert lint finding: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit lint findings: %w", err)
	}
	return nil
}

// DeleteFindingsByLayer clears one layer's findings before a fresh pass.
func (r *LintRepository) DeleteFindingsByLayer(ctx context.Context, layer entities.FindingLayer) error {
	if err := r.q.DeleteLintFindingsByLayer(ctx, string(layer)); err != nil {
		return fmt.Errorf("delete lint findings: %w", err)
	}
	return nil
}

// ListFindings lists findings, optionally filtered by kind.
func (r *LintRepository) ListFindings(ctx context.Context, kind string, limit, offset int) ([]entities.LintFinding, error) {
	var (
		rows []dal.LintFinding
		err  error
	)
	if kind == "" {
		rows, err = r.q.ListLintFindings(ctx, dal.ListLintFindingsParams{Limit: int64(limit), Offset: int64(offset)})
	} else {
		rows, err = r.q.ListLintFindingsByKind(ctx, dal.ListLintFindingsByKindParams{Kind: kind, Limit: int64(limit), Offset: int64(offset)})
	}
	if err != nil {
		return nil, fmt.Errorf("list lint findings: %w", err)
	}
	out := make([]entities.LintFinding, 0, len(rows))
	for _, row := range rows {
		out = append(out, toFinding(row))
	}
	return out, nil
}

// AtomsWithoutSource lists atoms with no provenance.
func (r *LintRepository) AtomsWithoutSource(ctx context.Context, limit int) ([]entities.Atom, error) {
	rows, err := r.q.ListAtomsWithoutSource(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("atoms without source: %w", err)
	}
	out := make([]entities.Atom, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.Atom{ID: row.ID, Claim: row.Claim})
	}
	return out, nil
}

// NotesWithoutProvenance lists pages that cite no atom (so they cannot be
func toFinding(row dal.LintFinding) entities.LintFinding {
	f := entities.LintFinding{
		ID:          idFromBytes(row.ID),
		RunID:       idFromBytes(row.RunID),
		Layer:       entities.FindingLayer(row.Layer),
		Kind:        row.Kind,
		Severity:    entities.FindingSeverity(row.Severity),
		SubjectKind: entities.SubjectKind(row.SubjectKind),
		Message:     row.Message,
		CreatedAt:   time.UnixMilli(row.CreatedAt).UTC(),
	}
	f.SubjectID = subjectString(row.SubjectID)
	return f
}

func idFromBytes(b []byte) ulid.ULID {
	var u ulid.ULID
	copy(u[:], b)
	return u
}

func subjectBytes(s string) []byte {
	if s == "" {
		return nil
	}
	if u, err := ulid.ParseStrict(s); err == nil {
		return u[:]
	}
	return []byte(s)
}

func subjectString(b []byte) string {
	if len(b) == 16 {
		var u ulid.ULID
		copy(u[:], b)
		return u.String()
	}
	return string(b)
}
