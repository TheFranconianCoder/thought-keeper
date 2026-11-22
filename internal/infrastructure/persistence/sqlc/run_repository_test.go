package sqlc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestRunTruncatedRoundTrip: a run that spent its research budget is still a succeeded run with an
// answer; the flag is what keeps that answer from looking complete after a restart.
func TestRunTruncatedRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewRunRepository(db)
	now := testClock{}.Now()
	run := entities.Run{
		ID: ulid.Make(), Op: entities.RunQuery, Status: entities.RunRunning,
		Question: "What about caching?", StartedAt: now,
	}
	if err := repo.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	run.Status = entities.RunSucceeded
	run.Answer = "Caching is subtle, and this answer is partial."
	run.Truncated = true
	run.EndedAt = now
	if err := repo.FinishRun(ctx, run); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	got, err := repo.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !got.Truncated {
		t.Error("truncated was not stored: a partial answer would read as a complete one")
	}
	if got.Status != entities.RunSucceeded || got.Answer == "" {
		t.Errorf("run = %+v, want a succeeded run with its answer", got)
	}
}
