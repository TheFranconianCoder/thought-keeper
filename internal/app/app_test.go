package app

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

func stalledApp() *App {
	return &App{
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		hub:           stream.NewHub(),
		stalled:       map[string]int{},
		progress:      map[string]runProgress{},
		distillActive: map[string]bool{},
		claimedAt:     map[string]time.Time{},
		queued:        map[string]bool{},
	}
}

// TestStallGuardStopsASelfRestartingSource is the guard for the observed loop: a completed run
// re-queued itself because the settle put the same claims back into the backlog, and each run
// "made progress" while the source never got further. Two runs without progress end the automatic
// re-derivation — the log says so and the user decides.
func TestStallGuardStopsASelfRestartingSource(t *testing.T) {
	a := stalledApp()
	hash := "abc"

	// First run: claims written, the same work left over.
	a.noteRunProgress(hash, 5, 3)
	if a.needsAttention(hash) {
		t.Fatal("a source that just made progress is already stalled")
	}
	// Second and third run: same numbers, no movement.
	a.noteRunProgress(hash, 0, 3)
	a.noteRunProgress(hash, 0, 3)
	if !a.needsAttention(hash) {
		t.Fatalf("the source kept restarting itself (stalled=%d)", a.stalled[hash])
	}
	// A user action clears it.
	a.clearStalled(hash)
	if a.needsAttention(hash) {
		t.Error("a reset or a pressed run must clear the stall")
	}
	// And a real improvement clears it by itself.
	a.noteRunProgress(hash, 0, 3)
	a.noteRunProgress(hash, 0, 3)
	a.noteRunProgress(hash, 0, 0) // the work is gone
	if a.needsAttention(hash) {
		t.Error("less work left is progress, not a stall")
	}
}

// TestQueuedIsNotLost: a run that waits in a queue is QUEUED, not running — and a claim with no
// queue and no worker behind it is stale and gets released. Without that distinction "running"
// in the UI was indistinguishable from a hang.
func TestQueuedIsNotLost(t *testing.T) {
	a := stalledApp()
	hash := "abc"
	if !a.claimDistill(hash) {
		t.Fatal("claim failed")
	}
	a.markQueued(hash, 3)
	if a.releaseStaleClaims(time.Now().Add(time.Hour), time.Minute) != 0 {
		t.Error("a queued run was treated as stale")
	}
	if a.RunningStep(hash) == "" {
		t.Error("a queued run must keep the claim")
	}
	// It reached a worker: no longer queued, and it is executing.
	a.markRunning(hash)
	if n := a.releaseStaleClaims(time.Now(), time.Minute); n != 0 {
		t.Errorf("a fresh run was released (%d)", n)
	}
	// The worker died without releasing: the claim is older than a whole run budget.
	if n := a.releaseStaleClaims(time.Now().Add(2*time.Hour), time.Minute); n != 1 {
		t.Fatalf("released %d stale claims, want 1", n)
	}
	if a.RunningStep(hash) != "" {
		t.Error("the stale claim was not released — the UI would say a step is running forever")
	}
}
