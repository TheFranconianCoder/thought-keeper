package app

import (
	"context"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
)

// stepsApp is the smallest App a barrier test needs: the step records are the only use case it reads.
func stepsApp(recs interfaces.StepTracker) *App {
	a := &App{}
	a.depsPtr.Store(&registry.Deps{Steps: recs})
	return a
}

type barrierSteps map[string]entities.SourceRecord

func (b barrierSteps) Record(context.Context, string, entities.Step, entities.StepStatus) error {
	return nil
}

func (b barrierSteps) Steps(string) (entities.StepSet, error)                { return entities.NewStepSet(), nil }
func (b barrierSteps) SetParts(string, map[string]entities.PartRecord) error { return nil }
func (b barrierSteps) Records() (map[string]entities.SourceRecord, error) {
	return b, nil
}
func (b barrierSteps) Reset(string) error                                  { return nil }
func (b barrierSteps) Remove(string) error                                 { return nil }
func (b barrierSteps) SetTitle(string, string) error                       { return nil }
func (b barrierSteps) SetLanguage(string, string) error                    { return nil }
func (b barrierSteps) SetPublished(string, time.Time, string) error        { return nil }
func (b barrierSteps) SetChanged(context.Context, string, time.Time) error { return nil }
func (b barrierSteps) SetOrigin(string, string, string) error              { return nil }
func (b barrierSteps) Release(string) error                                { return nil }
func (b barrierSteps) SetIdentity(string, entities.SourceIdentity) error   { return nil }
func (b barrierSteps) SetOwnership(string, string, string, string) error   { return nil }
func (b barrierSteps) Identity(string) (entities.SourceIdentity, error) {
	return entities.SourceIdentity{}, nil
}
func (b barrierSteps) Rehash(string, string) error { return nil }

// TestFeedAndSitemapAreNeverTheBarrier: a feed or a sitemap is a LIST of sources. Even when its
// record still says pending (a legacy record, a half-written refresh), it must not hold the
// outline, distill or cluster stages for the whole vault.
func TestFeedAndSitemapAreNeverTheBarrier(t *testing.T) {
	ok := entities.StepStatus{State: entities.StepOK}
	pending := entities.StepStatus{State: entities.StepPending}
	recs := barrierSteps{
		"feed": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindURL, RSS: true},
			Steps:    entities.NewStepSet().With(entities.StepCapture, pending),
		},
		"sitemap": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindURL, Sitemap: true},
			Steps:    entities.NewStepSet().With(entities.StepCapture, pending),
		},
		"page.md": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindMD},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, ok).With(entities.StepDistill, ok),
		},
	}
	a := stepsApp(recs)
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill, entities.StepCluster} {
		if !a.stepBarrierAllows(step) {
			t.Errorf("%s must not wait for a feed or a sitemap", step)
		}
	}
}

// TestACollectedLinkIsNeverTheBarrier: a bookmark is navigation, not knowledge — it has no document,
// so the outline and distill stages do not apply to it. Its record normally says "skipped · bookmark";
// a record that lost the entry (a re-add that wrote the fetch's answer over the reader's, then a reset
// that kept only the capture) must not become a barrier either. The pump would queue a tree build that
// returns without writing anything and claim it again on the next pass, forever — and that retry holds
// the outline stage, and behind it the distill of every other source.
func TestACollectedLinkIsNeverTheBarrier(t *testing.T) {
	ok := entities.StepStatus{State: entities.StepOK}
	recs := barrierSteps{
		"page.pdf": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, ok).With(entities.StepDistill, ok),
		},
		"link": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindURL, Bookmark: true},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok),
		},
	}
	a := stepsApp(recs)

	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill, entities.StepCluster} {
		if !a.stepBarrierAllows(step) {
			t.Errorf("%s must not wait for a collected link", step)
		}
	}

	// The record keeps saying "skipped · bookmark" once it is healed; the answer must not change.
	link := recs["link"]
	link.Steps = link.Steps.
		With(entities.StepOutline, entities.StepStatus{State: entities.StepSkipped}).
		With(entities.StepDistill, entities.StepStatus{State: entities.StepSkipped})
	recs["link"] = link
	if !a.stepBarrierAllows(entities.StepDistill) {
		t.Error("a bookmark whose lights are recorded as skipped must not hold distill")
	}
}

// TestAFailedStepDoesNotHoldTheBarrier: one unreadable page must not stop the cluster pass for the
// whole vault. A failed step is over for its source (it does not hold the others); the source itself
// cannot reach a later step until it is repaired, and its retry is the retry scheduler's job.
func TestAFailedStepDoesNotHoldTheBarrier(t *testing.T) {
	ok := entities.StepStatus{State: entities.StepOK}
	failed := entities.StepStatus{State: entities.StepFailed}
	recs := barrierSteps{
		"a.pdf": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, ok).With(entities.StepDistill, ok),
		},
		"b.pdf": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, ok).With(entities.StepDistill, failed),
		},
	}
	a := stepsApp(recs)

	if !a.stepBarrierAllows(entities.StepCluster) {
		t.Error("a failed distill must not hold the cluster step")
	}
	if a.sourceCannotReach(recs["b.pdf"], entities.StepDistill) {
		t.Error("a failed DISTILL must still be retried, not skipped")
	}

	broken := barrierSteps{
		"b.pdf": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, failed),
		},
	}
	a = stepsApp(broken)
	if !a.stepBarrierAllows(entities.StepDistill) {
		t.Error("a failed outline must not hold another source's distill")
	}
	if !a.sourceCannotReach(broken["b.pdf"], entities.StepDistill) {
		t.Error("a source whose outline failed cannot reach the distill")
	}
}

// TestAPendingPageFetchIsNotTheBarrier: a source whose page is being fetched is not derived yet and
// must not hold the vault. A derivation built on the copy that is leaving would be wrong the moment
// the page arrives, so the source waits — alone.
func TestAPendingPageFetchIsNotTheBarrier(t *testing.T) {
	ok := entities.StepStatus{State: entities.StepOK}
	pending := entities.StepStatus{State: entities.StepPending}
	recs := barrierSteps{
		"a.pdf": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF},
			Steps:    entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, ok).With(entities.StepDistill, ok),
		},
		"b.pdf": {
			Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF},
			Steps:    entities.NewStepSet().With(entities.StepCapture, pending),
		},
	}
	a := stepsApp(recs)
	a.urlPending = map[string]bool{"b.pdf": true}

	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill, entities.StepCluster} {
		if !a.stepBarrierAllows(step) {
			t.Errorf("%s must not wait for a source whose page is being fetched", step)
		}
	}

	// Once the fetch is over, the source is derived again and the barrier is real.
	a.clearURLPending("b.pdf")
	if a.stepBarrierAllows(entities.StepOutline) {
		t.Error("outline must wait for a source whose page fetch is over and its capture is not done")
	}
}

// TestGlobalStepBarrier: a later step is allowed only when EVERY source has completed every
// earlier document step. A single source behind blocks outline, distill and compile together.
func TestGlobalStepBarrier(t *testing.T) {
	ok := entities.StepStatus{State: entities.StepOK}
	pending := entities.StepStatus{State: entities.StepPending}
	recs := barrierSteps{
		"a.pdf": {Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF}, Steps: entities.NewStepSet().With(entities.StepCapture, ok).With(entities.StepOutline, ok).With(entities.StepDistill, ok)},
		"b.pdf": {Identity: entities.SourceIdentity{Kind: entities.SourceKindPDF}, Steps: entities.NewStepSet().With(entities.StepCapture, pending)},
	}
	a := stepsApp(recs)

	if a.stepBarrierAllows(entities.StepOutline) {
		t.Error("outline must wait for every source to finish capture")
	}
	if a.stepBarrierAllows(entities.StepDistill) {
		t.Error("distill must wait for every source to finish outline")
	}
	if a.stepBarrierAllows(entities.StepCluster) {
		t.Error("compile must wait for every source to finish distill")
	}

	b := recs["b.pdf"]
	b.Steps = b.Steps.With(entities.StepCapture, ok)
	recs["b.pdf"] = b
	if !a.stepBarrierAllows(entities.StepOutline) {
		t.Error("capture is complete for every source; outline may run")
	}
	if a.stepBarrierAllows(entities.StepDistill) {
		t.Error("distill must not start before the source has an outline")
	}

	b.Steps = b.Steps.With(entities.StepOutline, ok)
	recs["b.pdf"] = b
	if !a.stepBarrierAllows(entities.StepDistill) {
		t.Error("outline is complete for every source; distill may run")
	}
	if a.stepBarrierAllows(entities.StepCluster) {
		t.Error("compile must wait for the last distill")
	}

	b.Steps = b.Steps.With(entities.StepDistill, ok)
	recs["b.pdf"] = b
	if !a.stepBarrierAllows(entities.StepCluster) {
		t.Error("every document step is complete; compile may run")
	}
}
