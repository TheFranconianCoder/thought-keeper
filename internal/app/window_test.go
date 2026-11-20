package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// clockWindow is an "HH:MM-HH:MM" range that certainly covers the given instant.
func clockWindow(t time.Time, radius time.Duration) string {
	t = t.UTC()
	start, end := t.Add(-radius), t.Add(radius)
	return fmt.Sprintf("%02d:%02d-%02d:%02d", start.Hour(), start.Minute(), end.Hour(), end.Minute())
}

func windowApp(t *testing.T, windows ...string) *App {
	t.Helper()
	cfg := config.Defaults()
	cfg.OpenAI.BlockedWindows = windows
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.setConfig(cfg)
	a.depsPtr.Store(&registry.Deps{AgentAvailable: true})
	return a
}

// TestModelWindowRefusesAManualDerivation: a pressed Distill inside a window starts nothing and says
// when the derivation continues. The window is a state of the day, not a failure of the source.
func TestModelWindowRefusesAManualDerivation(t *testing.T) {
	a := windowApp(t, clockWindow(time.Now(), time.Hour))

	closed, reopen := a.modelWindowClosed()
	if !closed {
		t.Fatal("the window around now must be closed")
	}
	if reopen.IsZero() || !reopen.After(time.Now()) {
		t.Fatalf("reopen = %s, want an instant in the future", reopen)
	}

	_, err := a.ScheduleDistill("abc", false)
	if !errors.Is(err, types.ErrConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if !strings.Contains(err.Error(), "model window") {
		t.Fatalf("the refusal does not explain itself: %v", err)
	}
	if a.distillRunning("abc") {
		t.Fatal("a refused derivation claimed the source")
	}
}

// TestModelWindowIsOpenWithoutWindows: an empty list (or no window covering now) refuses nothing.
func TestModelWindowIsOpenWithoutWindows(t *testing.T) {
	if closed, _ := windowApp(t).modelWindowClosed(); closed {
		t.Fatal("no window configured, but the derivation is blocked")
	}
	if closed, _ := windowApp(t, clockWindow(time.Now().Add(6*time.Hour), time.Minute)).modelWindowClosed(); closed {
		t.Fatal("a window six hours away blocks the derivation now")
	}
}

// TestPumpHoldsTheDerivationInsideAWindow: the pump claims no source for the outline or the distill
// while a window is closed. Capture is local work and is not held.
func TestPumpHoldsTheDerivationInsideAWindow(t *testing.T) {
	a := windowApp(t, clockWindow(time.Now(), time.Hour))
	a.depsPtr.Store(&registry.Deps{
		AgentAvailable: true,
		Steps: barrierSteps{
			"page.md": {
				Identity: entities.SourceIdentity{Kind: entities.SourceKindMD},
				Steps:    entities.NewStepSet(), // capture is intake; outline and distill are owed
			},
		},
	})
	a.pumpOnce("test")
	if a.claimedDistills() != 0 {
		t.Fatal("the pump claimed a document inside the model window")
	}
	if a.distillRunning("page.md") {
		t.Fatal("the source was claimed for distill inside the model window")
	}
}

// TestJobNeedsModelNamesTheDerivation: the worker check must hold exactly the calls that go through
// the windowed gateway.
func TestJobNeedsModelNamesTheDerivation(t *testing.T) {
	for op, want := range map[entities.RunOp]bool{
		entities.RunPipeline:   true,
		entities.RunSummary:    true,
		entities.RunTag:        true,
		entities.RunVocabulary: true,
		entities.RunQuery:      false,
		entities.RunLint:       false,
	} {
		if got := jobNeedsModel(runJob{op: op}); got != want {
			t.Errorf("jobNeedsModel(%s) = %v, want %v", op, got, want)
		}
	}
}

// TestModelWindowWakeFiresAndClears: arming a wake for an instant that has already passed runs the
// resume once and clears the armed state, so a later pass can arm a new one.
func TestModelWindowWakeFiresAndClears(t *testing.T) {
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// No windows: the test is about the WAKE, and the default pair would re-arm it for the opening
	// of the real clock's window whenever the suite runs inside one.
	cfg := config.Defaults()
	cfg.OpenAI.BlockedWindows = nil
	a.setConfig(cfg)
	a.depsPtr.Store(&registry.Deps{})
	a.modelWindowPause(time.Now().Add(-time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.windowMu.Lock()
		armed := !a.windowWakeAt.IsZero()
		a.windowMu.Unlock()
		if !armed {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the wake did not fire (the armed instant is still set)")
}
