package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// fakeRuns records the run rows the app closes; the rest of the audit is irrelevant here.
type fakeRuns struct {
	finished []entities.Run
}

func (f *fakeRuns) CreateRun(context.Context, entities.Run) error { return nil }
func (f *fakeRuns) FinishRun(_ context.Context, r entities.Run) error {
	f.finished = append(f.finished, r)
	return nil
}
func (f *fakeRuns) AddStep(context.Context, entities.RunStep) error { return nil }
func (f *fakeRuns) GetRun(context.Context, ulid.ULID) (entities.Run, error) {
	return entities.Run{}, nil
}

func (f *fakeRuns) ListSteps(context.Context, ulid.ULID) ([]entities.RunStep, error) {
	return nil, nil
}

func (f *fakeRuns) AbandonRunningRuns(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

var _ interfaces.RunRepository = (*fakeRuns)(nil)

func claimApp(runs *fakeRuns) *App {
	a := &App{
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		distillActive: map[string]bool{},
		claimedAt:     map[string]time.Time{},
		queued:        map[string]bool{},
		claimedRun:    map[string]ulid.ULID{},
		executing:     map[string]entities.Step{},
		progress:      map[string]runProgress{},
		stalled:       map[string]int{},
		startRun:      usecases.StartRun{Runs: runs, Clock: systemClock{}},
	}
	return a
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// TestWatchdogKeepsARunningRun is the regression the log showed: a claim older than a whole run budget
// was released even though the run was EXECUTING, so the source was derived a second time and its step
// light reset. A big document legitimately runs for minutes.
func TestWatchdogKeepsARunningRun(t *testing.T) {
	runs := &fakeRuns{}
	a := claimApp(runs)

	const hash = "aaaa"
	if !a.claimDistill(hash) {
		t.Fatal("claim refused")
	}
	a.markRunning(hash) // the worker took it: not queued any more
	a.markExecuting(entities.StepDistill, hash)

	old := time.Now().Add(-2 * time.Hour)
	a.distillMu.Lock()
	a.claimedAt[hash] = old
	a.distillMu.Unlock()

	if n := a.releaseStaleClaims(time.Now(), time.Minute); n != 0 {
		t.Fatalf("released %d claims of a RUNNING run", n)
	}
	a.distillMu.Lock()
	active := a.distillActive[hash]
	a.distillMu.Unlock()
	if !active {
		t.Fatal("the claim of a running run was dropped")
	}

	// Once the step ends and nothing is queued, the same claim IS stale — and the run row it left
	// behind is closed instead of staying "running" for ever.
	a.clearExecuting(entities.StepDistill, hash)
	runID := ulid.Make()
	a.setClaimRun(hash, runID)
	if n := a.releaseStaleClaims(time.Now(), time.Minute); n != 1 {
		t.Fatalf("released %d claims, want the abandoned one", n)
	}
	if len(runs.finished) != 1 || runs.finished[0].ID != runID || runs.finished[0].Status != entities.RunAborted {
		t.Fatalf("the run behind the released claim was not closed: %+v", runs.finished)
	}
}

// TestWaitIsPerStep: "queued for the outline" and "queued for the distill" are two different waits. With
// one flag per source the tree worker's clearWaiting erased the distill's, and the watchdog then read a
// live claim as stale.
func TestWaitIsPerStep(t *testing.T) {
	a := claimApp(&fakeRuns{})
	const hash = "bbbb"

	a.markWaiting(entities.StepOutline, hash)
	a.markQueued(hash, 0) // the distill wait
	a.clearWaiting(entities.StepOutline, hash)

	if a.isQueued(entities.StepOutline, hash) {
		t.Fatal("the outline wait survived its worker")
	}
	if !a.isQueued(entities.StepDistill, hash) {
		t.Fatal("clearing the outline wait erased the distill wait")
	}
}

// TestOnePumpPassClaimsOnlyAWindow: a claim says "this source is being worked on", so one pass takes on
// as much of the distill backlog as the workers can handle (2 × parallel_runs + 2) — not all of it.
// Claiming 150 sources for 4 workers filled the queue, blocked a goroutine per overflow and let the
// watchdog expire claims nobody had started.
func TestOnePumpPassClaimsOnlyAWindow(t *testing.T) {
	const owed = 20
	records := barrierSteps{}
	for i := range owed {
		hash := fmt.Sprintf("hash%02d", i)
		records[hash] = entities.SourceRecord{
			Identity: entities.SourceIdentity{Kind: entities.SourceKindURL},
			Steps: entities.NewStepSet().
				With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}).
				With(entities.StepOutline, entities.StepStatus{State: entities.StepOK}).
				With(entities.StepDistill, entities.StepStatus{State: entities.StepPending}),
		}
	}
	a := claimApp(&fakeRuns{})
	a.steps = records
	a.cfg.Pipeline.ParallelRuns = 4
	a.cfg.Pipeline.AutoDistill = true
	a.distillQueue = make(chan runJob, 256)

	a.pumpOnce("test")

	a.distillMu.Lock()
	claimed := len(a.distillActive)
	a.distillMu.Unlock()
	if want := 2*4 + 2; claimed != want {
		t.Fatalf("one pass claimed %d sources, want the window of %d", claimed, want)
	}
	if len(a.distillQueue) != claimed {
		t.Fatalf("queued %d runs for %d claims", len(a.distillQueue), claimed)
	}
}
