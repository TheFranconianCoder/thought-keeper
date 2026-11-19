package usecases_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// readChanges returns the change record of a source, and whether it exists.
func readChanges(t *testing.T, store *objects.Store, hash string) (string, bool) {
	t.Helper()
	rc, err := store.OpenRendered(hash, entities.SidecarChanges)
	if err != nil {
		return "", false
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read change record: %v", err)
	}
	return string(b), true
}

// readSnapshot returns the bytes a source holds NOW, so a test can prove a check touched nothing.
func readSnapshot(t *testing.T, store *objects.Store, hash, ext string) string {
	t.Helper()
	rc, err := store.Open(hash, ext)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return string(b)
}

// changeRecordPage is long enough to be an indexed page rather than a bookmark.
const changeRecordPara = "alpha beta gamma delta epsilon zeta. "

func changeRecordPage(changed bool) string {
	line := changeRecordPara
	if changed {
		line = "alpha BETA gamma delta epsilon zeta. "
	}
	return "# Heading\n\n" + strings.Repeat(changeRecordPara, 7) + line + strings.Repeat(changeRecordPara, 30)
}

// TestRefreshWritesTheChangeRecord: a changed page leaves a record a reader can open — when, how many
// lines moved, and the diff itself. The first read and an unchanged check leave nothing behind.
func TestRefreshWritesTheChangeRecord(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	conv := &fakeConverter{title: "The Page", markdown: changeRecordPage(false)}
	uc := usecases.URLs{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{}}

	src, err := uc.Add(ctx, "https://example.com/changing", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, ok := readChanges(t, store, src.Hash); ok {
		t.Fatalf("a first read wrote a change record")
	}

	if _, err := uc.RefreshAll(ctx); err != nil {
		t.Fatalf("unchanged refresh: %v", err)
	}
	if _, ok := readChanges(t, store, src.Hash); ok {
		t.Fatalf("an unchanged check wrote a change record")
	}

	conv.markdown = changeRecordPage(true)
	if _, err := uc.RefreshAll(ctx); err != nil {
		t.Fatalf("changed refresh: %v", err)
	}
	record, ok := readChanges(t, store, src.Hash)
	if !ok {
		t.Fatalf("the changed page has no change record")
	}
	for _, want := range []string{
		"# Changed ",
		"Body: +1 -1 lines.",
		"+" + strings.TrimSuffix(changeRecordPara, "zeta. "),
		"alpha BETA gamma",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("record lacks %q:\n%s", want, record)
		}
	}
	if strings.Contains(record, "Title:") {
		t.Errorf("an unchanged title was reported as changed:\n%s", record)
	}

	// The record is about the LAST change: a title-only change replaces the body diff instead of
	// leaving a stale one beside a new title.
	conv.title = "The Page, renamed"
	if _, err := uc.RefreshAll(ctx); err != nil {
		t.Fatalf("title-only refresh: %v", err)
	}
	record, _ = readChanges(t, store, src.Hash)
	if !strings.Contains(record, `Title: "The Page" → "The Page, renamed"`) {
		t.Errorf("the title change is not in the record:\n%s", record)
	}
	if !strings.Contains(record, "Body: unchanged.") {
		t.Errorf("a title-only change claimed a body change:\n%s", record)
	}
	if strings.Contains(record, "```diff") {
		t.Errorf("the old body diff survived a title-only change:\n%s", record)
	}
}

// TestAttachWritesTheChangeRecord: the human replaced a snapshot, and the record says what the
// attachment replaced — the page that was there before.
func TestAttachWritesTheChangeRecord(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	conv := &fakeConverter{title: "The Page", markdown: changeRecordPage(false)}

	urls := usecases.URLs{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{}}
	src, err := urls.Add(ctx, "https://example.com/walled", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, ok := readChanges(t, store, src.Hash); ok {
		t.Fatalf("a first read wrote a change record")
	}

	attached := &fakeConverter{title: "The Page", markdown: changeRecordPage(true)}
	sources := usecases.Sources{Convert: attached, Store: store, Index: idx, Clock: fixedClock{}}
	if _, err := sources.Attach(ctx, usecases.AttachInput{
		Hash: src.Hash,
		Name: "page.html",
		Data: []byte("<html>the page the human sees</html>"),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	record, ok := readChanges(t, store, src.Hash)
	if !ok {
		t.Fatalf("an attached replacement wrote no change record")
	}
	if !strings.Contains(record, "Body: +1 -1 lines.") || !strings.Contains(record, "alpha BETA gamma") {
		t.Errorf("the attachment's change is not in the record:\n%s", record)
	}
}

// immaterialTail is a change that ticks on many pages without saying anything new: three words, one
// of them a single digit (which the tokenizer drops).
const immaterialTail = "\n\nUpdated 3 minutes ago."

// materialParagraph is real prose: twelve words, all long enough to pass the length rule.
const materialParagraph = "\n\nDatabase invalidation requires careful versioning strategies because stale entries quietly corrupt rendering."

// TestAnImmaterialChangeIsNotAnEvent: a page whose date counter ticked is not replaced at all. The
// stored snapshot is the baseline the outline and the claims describe, so the drift accumulates
// against it; the check time moves, a log line explains the silence, and no model is called.
func TestAnImmaterialChangeIsNotAnEvent(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	log := &fakeSourceLog{}
	steps := &fakeSteps{index: idx}
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	conv := &fakeConverter{title: "The Page", markdown: changeRecordPage(false)}
	uc := usecases.URLs{
		Fetch: fetcher, Convert: conv, Store: store, Index: idx, Steps: steps, SourceLog: log,
		Clock: fixedClock{}, ChangeMinWords: 10, ChangeMinWordLength: 5,
	}

	src, err := uc.Add(ctx, "https://example.com/counter", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	stored, storedMeta := readSnapshot(t, store, src.Hash, src.Ext), idx.m[src.Hash].Meta

	conv.markdown = changeRecordPage(false) + immaterialTail
	res, err := uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("immaterial refresh: %v", err)
	}
	if res.Changed != 1 || res.Material != 0 || res.Deferred != 1 {
		t.Fatalf("refresh = changed %d material %d deferred %d, want 1/0/1",
			res.Changed, res.Material, res.Deferred)
	}
	if len(res.DeferredHashes) != 1 || res.DeferredHashes[0] != src.Hash {
		t.Errorf("the deferred source is not named for the app: %v", res.DeferredHashes)
	}
	if len(res.Resets) != 0 {
		t.Errorf("a sub-threshold drift rebuilt the outline: %+v", res.Resets)
	}
	if after := readSnapshot(t, store, src.Hash, src.Ext); after != stored {
		t.Errorf("a sub-threshold drift replaced the snapshot")
	}
	if idx.m[src.Hash].Meta != storedMeta {
		t.Errorf("a sub-threshold drift rewrote the record: %s", idx.m[src.Hash].Meta)
	}
	if got, ok := steps.changed[src.Hash]; ok {
		t.Errorf("a sub-threshold drift moved the change time: %v", got)
	}
	if _, ok := readChanges(t, store, src.Hash); ok {
		t.Fatalf("a sub-threshold drift wrote a change record")
	}
	if !log.contains("below the threshold") {
		t.Errorf("the source log does not explain the missing update: %v", log.lines)
	}
	if idx.m[src.Hash].URLChecked.IsZero() {
		t.Errorf("the check was not recorded")
	}

	// A paragraph is a change a reader would notice. It replaces the snapshot, moves the time and
	// leaves the record — measured against the STORED page, so the counter that was deferred counts
	// too.
	conv.markdown = changeRecordPage(false) + immaterialTail + materialParagraph
	res, err = uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("material refresh: %v", err)
	}
	if res.Changed != 1 || res.Material != 1 || res.Deferred != 0 {
		t.Fatalf("refresh = changed %d material %d deferred %d, want 1/1/0",
			res.Changed, res.Material, res.Deferred)
	}
	if got := steps.changed[src.Hash]; !got.Equal(fixedClock{}.Now()) {
		t.Errorf("a material change did not move the change time: %v", got)
	}
	record, ok := readChanges(t, store, src.Hash)
	if !ok {
		t.Fatalf("a material change wrote no change record")
	}
	if !strings.Contains(record, "Words: 15 changed") {
		t.Errorf("the record does not name the measurement:\n%s", record)
	}
}
