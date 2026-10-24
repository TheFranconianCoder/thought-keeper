package app

import (
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

	a.resetSummaryMissing()
	a.mergeSummaryMissing([]string{"x", "x", ""})
	if got = a.summaryStatus(); got == nil || got.Missing != 1 {
		t.Fatalf("after a merge = %+v, want one missing source (deduplicated)", got)
	}

	a.resetSummaryMissing()
	a.summaryRunning = map[string]bool{}
	<-a.summaryQueue
	if got = a.summaryStatus(); got != nil {
		t.Fatalf("summary status = %+v after everything was written, want nil", got)
	}
}
