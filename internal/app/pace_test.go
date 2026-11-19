package app

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
)

// paceApp is the smallest App the update pace needs: the queues the pump fills, the admission
// ledger, and the step records it reads.
func paceApp(t *testing.T, gap string, recs interfaces.StepTracker) *App {
	t.Helper()
	a := &App{
		log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		treeQueue:        make(chan string, 8),
		captureQueue:     make(chan string, 8),
		treesInflight:    map[string]bool{},
		capturesInflight: map[string]bool{},
		distillActive:    map[string]bool{},
		admitted:         map[string]bool{},
		urlPending:       map[string]bool{},
		urlQueue:         make(chan urlJob, 8),
		autoURLQueue:     make(chan urlJob, 8),
		quit:             make(chan struct{}),
	}
	cfg := config.Defaults()
	cfg.Capture.RefreshMinGap = gap
	a.setConfig(cfg)
	a.depsPtr.Store(&registry.Deps{Steps: recs})
	return a
}

// awaitingOutline is a readable source whose capture is done and whose outline still owes work.
func awaitingOutline() entities.SourceRecord {
	return entities.SourceRecord{
		Identity: entities.SourceIdentity{Kind: entities.SourceKindMD, OriginalName: "note.md"},
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK}),
	}
}

// throughAllSteps is a source whose three document steps are done.
func throughAllSteps() entities.SourceRecord {
	ok := entities.StepStatus{State: entities.StepOK}
	return entities.SourceRecord{
		Identity: entities.SourceIdentity{Kind: entities.SourceKindMD, OriginalName: "note.md"},
		Steps: entities.NewStepSet().
			With(entities.StepCapture, ok).
			With(entities.StepOutline, ok).
			With(entities.StepDistill, ok),
	}
}

// TestPumpTakesOneSourcePerGap: with the pace on, a pass claims ONE source, and the next source
// waits for the next turn — even though the pump visits both.
func TestPumpTakesOneSourcePerGap(t *testing.T) {
	recs := barrierSteps{"aaaa": awaitingOutline(), "bbbb": awaitingOutline()}
	a := paceApp(t, "1h", recs)

	a.pumpOnce("test")
	if got := len(a.treeQueue); got != 1 {
		t.Fatalf("the pass queued %d outlines, want 1", got)
	}
	a.pumpOnce("test")
	if got := len(a.treeQueue); got != 1 {
		t.Fatalf("a second pass took another source in: %d outlines queued", got)
	}
}

// TestABookedSourceNeedsNoTurn: the fetch already took the source in (or a human's action is not
// paced), so the pump claims it without waiting for a turn.
func TestABookedSourceNeedsNoTurn(t *testing.T) {
	recs := barrierSteps{"aaaa": awaitingOutline()}
	a := paceApp(t, "1h", recs)
	a.bookAdmission("aaaa")

	a.pumpOnce("test")
	if got := len(a.treeQueue); got != 1 {
		t.Fatalf("a booked source was not claimed: %d queued", got)
	}
}

// TestTheTurnExpires: the pace is a rate, not a lock: after the gap the next source may be taken in.
func TestTheTurnExpires(t *testing.T) {
	a := paceApp(t, "250ms", barrierSteps{})
	if !a.takeAdmission() {
		t.Fatal("the first turn was refused")
	}
	if a.takeAdmission() {
		t.Fatal("a second source was taken in inside the same gap")
	}
	time.Sleep(300 * time.Millisecond)
	if !a.takeAdmission() {
		t.Fatal("the turn did not come back after the gap")
	}
}

// TestAnIdleLookGivesTheTurnBack: the fetch worker takes the turn before it knows whether anything
// is due. A look that fetched nothing must not spend the turn on the pump's next source.
func TestAnIdleLookGivesTheTurnBack(t *testing.T) {
	a := paceApp(t, "1h", barrierSteps{})
	prev, ok := a.awaitAdmission()
	if !ok {
		t.Fatal("the first turn was refused")
	}
	a.releaseAdmission(prev)
	if !a.takeAdmission() {
		t.Fatal("an idle look kept the turn")
	}
}

// TestABurstTakesInEverything: empty or "0s" is the old burst — one pass claims the whole backlog.
func TestABurstTakesInEverything(t *testing.T) {
	recs := barrierSteps{"aaaa": awaitingOutline(), "bbbb": awaitingOutline()}
	a := paceApp(t, "", recs)

	a.pumpOnce("test")
	if got := len(a.treeQueue); got != 2 {
		t.Fatalf("the burst queued %d outlines, want 2", got)
	}
}

// TestAFinishedSourceIsForgotten: once a source's steps are through it leaves the ledger, so its
// next update takes a fresh turn.
func TestAFinishedSourceIsForgotten(t *testing.T) {
	a := paceApp(t, "1h", barrierSteps{})
	a.bookAdmission("aaaa")
	if !a.admissionBooked("aaaa") {
		t.Fatal("booking did not stick")
	}
	a.forgetFinishedAdmissions(barrierSteps{"aaaa": throughAllSteps()}, nil)
	if a.admissionBooked("aaaa") {
		t.Fatal("a finished source stayed booked")
	}
}

// TestDiscoveredPagesArePacedButTheHumansAreNot: a page a sitemap or feed declared lands in the paced
// queue; a URL the human pasted keeps the three workers and acts at once.
func TestDiscoveredPagesArePacedButTheHumansAreNot(t *testing.T) {
	a := paceApp(t, "1h", barrierSteps{})

	if !a.enqueueURL(urlJob{URL: "https://example.com/page", Origin: entities.SitemapOrigin}) {
		t.Fatal("the discovered page was not queued")
	}
	select {
	case job := <-a.autoURLQueue:
		if job.Origin != entities.SitemapOrigin {
			t.Fatalf("the paced queue got origin %q", job.Origin)
		}
	default:
		t.Fatal("a discovered page did not reach the paced queue")
	}
	if len(a.urlQueue) != 0 {
		t.Fatal("a discovered page landed in the human's queue")
	}

	if !a.enqueueURL(urlJob{URL: "https://example.com/mine"}) {
		t.Fatal("the human's URL was not queued")
	}
	select {
	case job := <-a.urlQueue:
		if job.URL != "https://example.com/mine" {
			t.Fatalf("the human's queue got %q", job.URL)
		}
	default:
		t.Fatal("the human's URL did not reach the immediate queue")
	}
}
