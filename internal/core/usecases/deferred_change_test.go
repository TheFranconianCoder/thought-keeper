package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// prosePage builds a page of n words, each long enough to pass the length rule. Repeated words keep
// the measurement exact: a change of k words is a difference of k positions, whatever the order.
func prosePage(n int) string {
	return "# Heading\n\n" + strings.Repeat("database ", n)
}

// driftCase is one refreshable page with the threshold knobs a test wants.
func driftCase(t *testing.T, markdown string, minWords, minPercent int) (usecases.URLs, *fakeConverter, *fakeIndex, *objects.Store, *fakeSourceLog, *fakeSteps, string) {
	t.Helper()
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	log := &fakeSourceLog{}
	steps := &fakeSteps{index: idx}
	conv := &fakeConverter{title: "The Page", markdown: markdown}
	uc := usecases.URLs{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}},
		Convert: conv, Store: store, Index: idx, Steps: steps, SourceLog: log,
		Clock:            fixedClock{},
		ChangeMinWords:   minWords,
		ChangeMinPercent: minPercent, ChangeMinWordLength: 5,
	}
	src, err := uc.Add(ctx, "https://example.com/drifting", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return uc, conv, idx, store, log, steps, src.Hash
}

// TestADriftBelowTheThresholdKeepsTheSnapshot: the stored bytes are the baseline of the derivation,
// so a change nobody would notice must not replace them — and must not start the pipeline.
func TestADriftBelowTheThresholdKeepsTheSnapshot(t *testing.T) {
	ctx := context.Background()
	uc, conv, idx, store, log, steps, hash := driftCase(t, prosePage(400), 40, 1)
	src, err := idx.GetSourceByHash(ctx, hash)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	stored, storedMeta := readSnapshot(t, store, src.Hash, src.Ext), src.Meta

	conv.markdown = prosePage(420) // +20 words: below the floor, above the share
	res, err := uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Changed != 1 || res.Material != 0 || res.Deferred != 1 {
		t.Fatalf("refresh = changed %d material %d deferred %d, want 1/0/1",
			res.Changed, res.Material, res.Deferred)
	}
	if len(res.Resets) != 0 {
		t.Errorf("a sub-threshold drift rebuilt the outline: %+v", res.Resets)
	}
	if len(res.DeferredHashes) != 1 || res.DeferredHashes[0] != hash {
		t.Errorf("the deferred source is not named: %v", res.DeferredHashes)
	}
	if after := readSnapshot(t, store, hash, src.Ext); after != stored {
		t.Errorf("a sub-threshold drift replaced the snapshot")
	}
	if after := idx.m[hash].Meta; after != storedMeta {
		t.Errorf("a sub-threshold drift rewrote the record:\n%s\nwas:\n%s", after, storedMeta)
	}
	if got, ok := steps.changed[hash]; ok {
		t.Errorf("a sub-threshold drift moved the change time: %v", got)
	}
	if idx.m[hash].URLChecked.IsZero() {
		t.Errorf("the check was not recorded")
	}
	if !log.contains("below the threshold") {
		t.Errorf("the source log does not explain the missing update: %v", log.lines)
	}
}

// TestASmallDriftAccumulatesUntilItIsAnEvent: two checks of +20 words on a 400-word page are one
// event, not two — because the second is measured against the bytes the claims describe.
func TestASmallDriftAccumulatesUntilItIsAnEvent(t *testing.T) {
	ctx := context.Background()
	uc, conv, _, store, _, _, hash := driftCase(t, prosePage(400), 40, 0)

	conv.markdown = prosePage(420) // +20
	if res, err := uc.RefreshAll(ctx); err != nil {
		t.Fatalf("first drift: %v", err)
	} else if res.Deferred != 1 || res.Material != 0 || len(res.Resets) != 0 {
		t.Fatalf("first drift = deferred %d material %d resets %d, want 1/0/0",
			res.Deferred, res.Material, len(res.Resets))
	}

	conv.markdown = prosePage(440) // +40 against the STORED page: the floor is reached
	res, err := uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("second drift: %v", err)
	}
	if res.Material != 1 || res.Deferred != 0 {
		t.Fatalf("second drift = deferred %d material %d, want 0/1", res.Deferred, res.Material)
	}
	if len(res.Resets) != 1 {
		t.Fatalf("an event did not rebuild the outline: %+v", res.Resets)
	}
	if got := readSnapshot(t, store, hash, ".md"); !strings.Contains(got, strings.TrimSpace(prosePage(440))) {
		t.Errorf("the event did not replace the snapshot")
	}
	if _, ok := readChanges(t, store, hash); !ok {
		t.Errorf("the event wrote no change record")
	}
}

// TestTheShareRuleKeepsALongPageQuiet: a long page needs more than the floor, because re-deriving it
// costs more. The share is what a page of six thousand words buys.
func TestTheShareRuleKeepsALongPageQuiet(t *testing.T) {
	ctx := context.Background()
	uc, conv, _, store, _, _, hash := driftCase(t, prosePage(6000), 40, 1) // required: 60 words
	stored := readSnapshot(t, store, hash, ".md")

	conv.markdown = prosePage(6045) // +45: above the floor, below the share
	res, err := uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Deferred != 1 || res.Material != 0 {
		t.Fatalf("a 45-word drift on 6 000 words = deferred %d material %d, want 1/0",
			res.Deferred, res.Material)
	}
	if after := readSnapshot(t, store, hash, ".md"); after != stored {
		t.Errorf("the share rule did not keep the snapshot")
	}

	conv.markdown = prosePage(6070) // +70 total
	res, err = uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if res.Material != 1 || res.Deferred != 0 {
		t.Fatalf("a 70-word drift on 6 000 words = deferred %d material %d, want 0/1",
			res.Deferred, res.Material)
	}
}

// TestTheRefreshButtonAppliesASmallChange: the human asked for THIS page, so its answer is applied
// however small the difference is.
func TestTheRefreshButtonAppliesASmallChange(t *testing.T) {
	ctx := context.Background()
	uc, conv, _, store, _, _, hash := driftCase(t, prosePage(400), 40, 1)

	conv.markdown = prosePage(410) // +10: a scheduled pass keeps the snapshot
	res, err := uc.RefreshOne(ctx, hash)
	if err != nil {
		t.Fatalf("refresh one: %v", err)
	}
	if res.Material != 1 || res.Deferred != 0 {
		t.Fatalf("refresh one = deferred %d material %d, want 0/1", res.Deferred, res.Material)
	}
	if len(res.Resets) != 1 || !strings.Contains(res.Resets[0].Reason, "you refreshed") {
		t.Fatalf("the reason does not say who asked: %+v", res.Resets)
	}
	if got := readSnapshot(t, store, hash, ".md"); !strings.Contains(got, strings.TrimSpace(prosePage(410))) {
		t.Errorf("the button did not replace the snapshot")
	}
	if _, ok := readChanges(t, store, hash); !ok {
		t.Errorf("the button wrote no change record")
	}
}
