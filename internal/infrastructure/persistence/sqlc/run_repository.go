package sqlc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc/dal"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// RunRepository implements interfaces.RunRepository (append-only audit).
type RunRepository struct {
	db *sql.DB
	q  *dal.Queries
}

var _ interfaces.RunRepository = (*RunRepository)(nil)

// NewRunRepository wraps a database handle.
func NewRunRepository(db *sql.DB) *RunRepository {
	return &RunRepository{db: db, q: dal.New(db)}
}

// CreateRun inserts a run row.
func (r *RunRepository) CreateRun(ctx context.Context, run entities.Run) error {
	if err := r.q.InsertRun(ctx, dal.InsertRunParams{
		ID:               run.ID,
		Op:               string(run.Op),
		Step:             string(run.Step),
		Model:            run.Model,
		TargetID:         []byte(run.TargetID),
		Status:           string(run.Status),
		PromptTokens:     int64(run.PromptTokens),
		CompletionTokens: int64(run.CompletionTokens),
		TotalTokens:      int64(run.TotalTokens),
		Iterations:       int64(run.Iterations),
		Question:         run.Question,
		Answer:           run.Answer,
		Error:            run.Error,
		StartedAt:        run.StartedAt.UnixMilli(),
		EndedAt:          nullTime(run.EndedAt),
	}); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

// FinishRun updates a run's terminal fields.
func (r *RunRepository) FinishRun(ctx context.Context, run entities.Run) error {
	if err := r.q.FinishRun(ctx, dal.FinishRunParams{
		Status:           string(run.Status),
		PromptTokens:     int64(run.PromptTokens),
		CompletionTokens: int64(run.CompletionTokens),
		TotalTokens:      int64(run.TotalTokens),
		Iterations:       int64(run.Iterations),
		Answer:           run.Answer,
		Error:            run.Error,
		EndedAt:          nullTime(run.EndedAt),
		ID:               run.ID,
	}); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// AbandonRunningRuns closes every run still marked running. It is the start-up repair: the process that
// opened those rows is gone, so no worker is behind them — left as they are, the Job Queue and the run
// history claim work that nobody is doing.
func (r *RunRepository) AbandonRunningRuns(ctx context.Context, reason string, at time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, abandonRunningRunsSQL, reason, at.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("abandon running runs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

const abandonRunningRunsSQL = `UPDATE runs SET status = 'aborted', error = ?, ended_at = ? WHERE status = 'running'`

// AddStep appends one tool-call step.
func (r *RunRepository) AddStep(ctx context.Context, s entities.RunStep) error {
	if err := r.q.InsertRunStep(ctx, dal.InsertRunStepParams{
		RunID:        s.RunID[:],
		Seq:          int64(s.Seq),
		Tool:         s.Tool,
		ArgsDigest:   s.ArgsDigest,
		ResultDigest: s.ResultDigest,
		IsError:      boolInt(s.IsError),
		DurationMs:   int64(s.DurationMS),
	}); err != nil {
		return fmt.Errorf("insert run step: %w", err)
	}
	return nil
}

// GetRun returns a run by id.
func (r *RunRepository) GetRun(ctx context.Context, id ulid.ULID) (entities.Run, error) {
	row, err := r.q.GetRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return entities.Run{}, types.New("not_found", "no such run", types.ErrNotFound)
	}
	if err != nil {
		return entities.Run{}, fmt.Errorf("get run: %w", err)
	}
	run := entities.Run{
		ID:               row.ID,
		Op:               entities.RunOp(row.Op),
		Step:             entities.Step(row.Step),
		Model:            row.Model,
		TargetID:         string(row.TargetID),
		Status:           entities.RunStatus(row.Status),
		PromptTokens:     int(row.PromptTokens),
		CompletionTokens: int(row.CompletionTokens),
		TotalTokens:      int(row.TotalTokens),
		Iterations:       int(row.Iterations),
		Question:         row.Question,
		Answer:           row.Answer,
		Error:            row.Error,
		StartedAt:        time.UnixMilli(row.StartedAt).UTC(),
	}
	if row.EndedAt.Valid {
		run.EndedAt = time.UnixMilli(row.EndedAt.Int64).UTC()
	}
	return run, nil
}

// ListSteps returns a run's tool-call steps in order.
func (r *RunRepository) ListSteps(ctx context.Context, id ulid.ULID) ([]entities.RunStep, error) {
	rows, err := r.q.ListRunSteps(ctx, id[:])
	if err != nil {
		return nil, fmt.Errorf("list run steps: %w", err)
	}
	out := make([]entities.RunStep, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.RunStep{
			Seq:        int(row.Seq),
			Tool:       row.Tool,
			IsError:    row.IsError != 0,
			DurationMS: int(row.DurationMs),
		})
	}
	return out, nil
}

func nullTime(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}
