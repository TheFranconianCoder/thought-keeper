package app

import (
	"context"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

type barrierSteps map[string]entities.SourceRecord

func (b barrierSteps) Record(context.Context, string, entities.Step, entities.StepStatus) error {
	return nil
}

func (b barrierSteps) Steps(string) (entities.StepSet, error)                { return entities.NewStepSet(), nil }
func (b barrierSteps) SetParts(string, map[string]entities.PartRecord) error { return nil }
func (b barrierSteps) Records() (map[string]entities.SourceRecord, error) {
	return b, nil
}
func (b barrierSteps) Reset(string) error                                { return nil }
func (b barrierSteps) Remove(string) error                               { return nil }
func (b barrierSteps) SetTitle(string, string) error                     { return nil }
func (b barrierSteps) SetLanguage(string, string) error                  { return nil }
func (b barrierSteps) SetOrigin(string, string, string) error            { return nil }
func (b barrierSteps) Release(string) error                              { return nil }
func (b barrierSteps) SetIdentity(string, entities.SourceIdentity) error { return nil }
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
	a := &App{steps: recs}
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill, entities.StepCluster} {
		if !a.stepBarrierAllows(step) {
			t.Errorf("%s must not wait for a feed or a sitemap", step)
		}
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
	a := &App{steps: recs}

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
