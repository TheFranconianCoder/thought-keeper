package app

import (
	"slices"
	"testing"
	"time"
)

// TestSummaryStatusReportsTheBackfill: the Status lens shows the summary backfill while it runs —
// the backlog the consistency walk measured, what is queued, and what the one summary worker owns
// right now. A completed summary leaves the backlog immediately; an empty backfill is nil, so the
// block does not linger after everything is written.
func TestSummaryStatusReportsTheBackfill(t *testing.T) {
	a := &App{
		summaryQueue:       make(chan runJob, 4),
		summaryRemaining:   map[string]bool{"a": true, "b": true, "c": true},
		summaryRunning:     map[string]bool{"a": true},
		summaryRemainingAt: time.UnixMilli(1_700_000_000_000).UTC(),
	}
	a.summaryQueue <- runJob{}

	got := a.summaryStatus()
	if got == nil {
		t.Fatal("summary status = nil, want the running backfill")
	}
	if got.Missing != 3 || got.Queued != 1 || got.Running != 1 {
		t.Fatalf("summary status = %+v, want missing 3, queued 1, running 1", got)
	}
	if got.At != "2023-11-14T22:13:20Z" {
		t.Errorf("at = %q, want the measurement time", got.At)
	}

	a.completeSummary("a")
	got = a.summaryStatus()
	if got == nil || got.Missing != 2 || got.Running != 0 {
		t.Fatalf("after a completion = %+v, want missing 2 and nothing running", got)
	}

	// A walk adds to the backlog while it runs, and publishes what it measured only when it ends:
	// "b" and "c" stay visible until then, so the number does not dip to "what the first slice found".
	a.startSummaryWalk()
	a.mergeSummaryMissing([]string{"x", "x", ""})
	if got = a.summaryStatus(); got == nil || got.Missing != 3 {
		t.Fatalf("after a merge = %+v, want the three owed sources (deduplicated)", got)
	}
	a.finishSummaryWalk()
	if got = a.summaryStatus(); got == nil || got.Missing != 1 {
		t.Fatalf("after the walk ended = %+v, want only what the walk found", got)
	}

	a.completeSummary("x")
	a.summaryRunning = map[string]bool{}
	<-a.summaryQueue
	if got = a.summaryStatus(); got != nil {
		t.Fatalf("summary status = %+v after everything was written, want nil", got)
	}
}

// TestTheBackfillsAreFedFromTheWalkNotFromItsSlice: the consistency pass walks the vault in slices
// (`reconcile_batch`) while a backfill queues a few runs per PASS. Handing the request function only
// the slice it just read made the budget per SLICE: the 385 overviews a burst-distilled vault owed sat
// in two slices, which handed out their two each — four per seven-minute walk. The backlog spans the
// walk (a slice merge adds to it) and a completed run leaves it at once, so the next pass asks for the
// next two from the WHOLE backlog. It is also kept apart from what the Status lens shows until the
// walk ends, so that number never falls back to "what the first slice found".
func TestTheBackfillsAreFedFromTheWalkNotFromItsSlice(t *testing.T) {
	a := &App{
		summaryRemaining:   map[string]bool{},
		summaryRunning:     map[string]bool{},
		sourceTagRemaining: map[string]bool{},
	}

	// Walk 1, slice 1 finds two, slice 2 finds one. Each slice is one tick.
	a.startSummaryWalk()
	a.resetSourceTagMissing()
	a.mergeSummaryMissing([]string{"a", "b"})
	a.mergeSourceTagMissing([]string{"a", "b"})
	a.mergeSummaryMissing([]string{"c"})
	a.mergeSourceTagMissing([]string{"c"})

	if got, want := a.summaryBacklog(), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("summary backlog after two slices = %v, want %v", got, want)
	}
	if got, want := a.sourceTagBacklog(), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("tag backlog after two slices = %v, want %v", got, want)
	}

	// A finished run (or one that failed and waits for the next walk) leaves the backlog, whatever
	// slice the walk is looking at.
	a.completeSummary("a")
	a.completeSourceTag("a")
	if got, want := a.summaryBacklog(), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("summary backlog after a completion = %v, want %v", got, want)
	}
	if got, want := a.sourceTagBacklog(), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("tag backlog after a completion = %v, want %v", got, want)
	}

	// The tag backlog measures from scratch on a new walk. The summary backlog does not: a walk is
	// kept apart and swapped in when it ends, so the number the reader sees never falls to "the first
	// slice" and climbs back up.
	a.startSummaryWalk()
	a.resetSourceTagMissing()
	if got := a.sourceTagBacklog(); len(got) != 0 {
		t.Errorf("tag backlog after a fresh walk started = %v, want empty", got)
	}
	if got, want := a.summaryBacklog(), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("summary backlog after a fresh walk started = %v, want the last walk's %v", got, want)
	}
	a.mergeSummaryMissing([]string{"b"})
	a.finishSummaryWalk()
	if got, want := a.summaryBacklog(), []string{"b"}; !slices.Equal(got, want) {
		t.Errorf("summary backlog after the walk ended = %v, want what it measured (%v)", got, want)
	}
}

// TestTheBackfillsKeepTheirWorkerBusy: the backfills are watermarks, not rates. The summary worker
// takes ~7 s per overview, so a fixed trickle of two a minute left it idle four fifths of the time; a
// watermark keeps it fed (every finished run hands over the next one) while leaving most of the queue
// free — the reader's Generate button must never be told "full" because a backlog is draining.
func TestTheBackfillsKeepTheirWorkerBusy(t *testing.T) {
	a := &App{
		summaryQueue:    make(chan runJob, 16),
		summaryActive:   map[string]bool{},
		sourceTagActive: map[string]bool{},
	}
	if got := a.summaryBackfillBudget(); got != summaryBackfillOutstanding {
		t.Fatalf("summary budget with nothing in flight = %d, want %d", got, summaryBackfillOutstanding)
	}
	if summaryBackfillOutstanding >= cap(a.summaryQueue) {
		t.Errorf("the summary backfill watermark (%d) leaves no room in the %d-place queue",
			summaryBackfillOutstanding, cap(a.summaryQueue))
	}
	for i := range summaryBackfillOutstanding {
		a.summaryActive[string(rune('a'+i))] = true
	}
	if got := a.summaryBackfillBudget(); got != 0 {
		t.Errorf("summary budget at the watermark = %d, want 0", got)
	}
	a.releaseSummary("a")
	if got := a.summaryBackfillBudget(); got != 1 {
		t.Errorf("summary budget after one finished = %d, want 1", got)
	}

	if got := a.sourceTagBackfillBudget(); got != sourceTagBackfillOutstanding {
		t.Fatalf("tag budget with nothing in flight = %d, want %d", got, sourceTagBackfillOutstanding)
	}
	for i := range sourceTagBackfillOutstanding {
		a.sourceTagActive[string(rune('a'+i))] = true
	}
	if got := a.sourceTagBackfillBudget(); got != 0 {
		t.Errorf("tag budget at the watermark = %d, want 0", got)
	}
	a.releaseSourceTag("a")
	if got := a.sourceTagBackfillBudget(); got != 1 {
		t.Errorf("tag budget after one finished = %d, want 1", got)
	}
}
