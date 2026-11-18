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

// TestAnImmaterialChangeIsNotAnEvent: a page whose date counter ticked is still replaced (the vault
// stays current) and still rebuilt (the outline's byte references move), but it does not move the
// change time, does not write a record, and does not mark the source updated.
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

	conv.markdown = changeRecordPage(false) + immaterialTail
	res, err := uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("immaterial refresh: %v", err)
	}
	if res.Changed != 1 || res.Material != 0 {
		t.Fatalf("refresh = changed %d material %d, want 1/0", res.Changed, res.Material)
	}
	if got, ok := steps.changed[src.Hash]; ok {
		t.Errorf("a ticked counter moved the change time: %v", got)
	}
	if _, ok := readChanges(t, store, src.Hash); ok {
		t.Fatalf("an immaterial change wrote a change record")
	}
	if len(res.Resets) != 1 {
		t.Errorf("the first body change did not rebuild the outline: %+v", res.Resets)
	}
	if !log.contains("immaterially") {
		t.Errorf("the source log does not explain the missing mark: %v", log.lines)
	}

	// A paragraph is a change a reader would notice. It moves the time and leaves the record.
	conv.markdown = changeRecordPage(false) + immaterialTail + materialParagraph
	res, err = uc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("material refresh: %v", err)
	}
	if res.Changed != 1 || res.Material != 1 {
		t.Fatalf("refresh = changed %d material %d, want 1/1", res.Changed, res.Material)
	}
	if got := steps.changed[src.Hash]; !got.Equal(fixedClock{}.Now()) {
		t.Errorf("a material change did not move the change time: %v", got)
	}
	record, ok := readChanges(t, store, src.Hash)
	if !ok {
		t.Fatalf("a material change wrote no change record")
	}
	if !strings.Contains(record, "Words: 12 changed") {
		t.Errorf("the record does not name the measurement:\n%s", record)
	}
}
