package app

import (
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestPipelineStatusAnswers guards the one bug this function can have and did have: collecting the jobs
// from INSIDE the pipe lock. A mutex is not reentrant, so that self-deadlocks — and the app stops
// answering at its very first status call, which looks like a hang, not like a mistake.
//
// The test hangs (and fails on the package timeout) if the nesting comes back.
func TestPipelineStatusAnswers(t *testing.T) {
	a := &App{
		distillActive:    map[string]bool{"7f3a": true},
		claimedAt:        map[string]time.Time{"7f3a": time.Now()},
		queued:           map[string]bool{queueKey(entities.StepDistill, "7f3a"): true},
		capturesInflight: map[string]bool{"aaaa": true},
		treesInflight:    map[string]bool{"bbbb": true},
	}
	p := a.PipelineStatus()
	if len(p.Jobs) != 3 {
		t.Fatalf("jobs = %+v, want the three claimed sources", p.Jobs)
	}
	for _, j := range p.Jobs {
		if j.Target == "7f3a" && j.State != "queued" {
			t.Errorf("the queued source is not marked queued: %+v", j)
		}
		if j.Target == "bbbb" && j.Why != "taken by its worker: checking the step barrier" {
			// Nothing holds the gate here. "Waiting for the vault gate" sent a reader looking at the
			// gate while one collected link was retried, so the fallback says what it can prove.
			t.Errorf("a job taken but not executing must not blame the vault gate: %+v", j)
		}
	}
	if p.Jobs[0].Since == "" && p.Jobs[1].Since == "" && p.Jobs[2].Since == "" {
		t.Errorf("no job carries the time it was claimed: %+v", p.Jobs)
	}
}
