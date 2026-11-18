package usecases_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// seedURL puts one URL source in the in-memory index, as the cadence would find it.
func seedURL(idx *fakeIndex, rawURL string, checkedAgo time.Duration, meta string) entities.Source {
	hash := entities.URLHash(rawURL)
	now := fixedClock{}.Now()
	src := entities.Source{
		ID:           ulid.Make(),
		Hash:         hash,
		Kind:         entities.SourceKindURL,
		Ext:          ".md",
		URL:          rawURL,
		Title:        rawURL,
		ImportedAt:   now.Add(-30 * 24 * time.Hour),
		URLChecked:   now.Add(-checkedAgo),
		Meta:         meta,
		OriginalName: rawURL,
	}
	idx.m[hash] = src
	return src
}

// pacedUC is a refresher with the weekly page cadence and the daily feed cadence.
func pacedUC(t *testing.T, idx *fakeIndex, fetcher interfaces.Fetcher, conv interfaces.HTMLConverter) usecases.URLs {
	t.Helper()
	return usecases.URLs{
		Fetch:      fetcher,
		Convert:    conv,
		Store:      objects.New(t.TempDir()),
		Index:      idx,
		Clock:      fixedClock{},
		MinAge:     168 * time.Hour,
		FeedMinAge: 24 * time.Hour,
	}
}

// TestRefreshNextPicksTheOldestDueSource: a paced pass checks ONE page, and the one that has waited
// longest — not the newest arrival (which is what the batch order would hand back first).
func TestRefreshNextPicksTheOldestDueSource(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	oldest := seedURL(idx, "https://example.com/oldest", 30*24*time.Hour, "")
	newer := seedURL(idx, "https://example.com/newer", 10*24*time.Hour, "")
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	uc := pacedUC(t, idx, fetcher, &fakeConverter{title: "T", markdown: strings.Repeat("body ", 100)})

	first, found, err := uc.RefreshNext(ctx)
	if err != nil || !found {
		t.Fatalf("refresh next: found=%v err=%v", found, err)
	}
	if first.Checked != 1 {
		t.Fatalf("a paced pass checked %d pages, want 1", first.Checked)
	}
	if got := idx.m[oldest.Hash].URLChecked; !got.Equal(fixedClock{}.Now()) {
		t.Errorf("the oldest was not the one checked: %v", got)
	}
	if got := idx.m[newer.Hash].URLChecked; got.Equal(fixedClock{}.Now()) {
		t.Errorf("the newer source was checked first")
	}

	second, found, err := uc.RefreshNext(ctx)
	if err != nil || !found || second.Checked != 1 {
		t.Fatalf("second pass: %+v found=%v err=%v", second, found, err)
	}
	if got := idx.m[newer.Hash].URLChecked; !got.Equal(fixedClock{}.Now()) {
		t.Errorf("the second call did not reach the newer source: %v", got)
	}
}

// TestRefreshNextSkipsAnAttachedSnapshot: an attachment exists because the page cannot be read; the
// cadence never fetches it, and a paced worker must not spin on it either.
func TestRefreshNextSkipsAnAttachedSnapshot(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	attached := seedURL(idx, "https://example.com/attached", 60*24*time.Hour, `{"attached":true}`)
	mine := seedURL(idx, "https://example.com/mine", 10*24*time.Hour, "")
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	uc := pacedUC(t, idx, fetcher, &fakeConverter{title: "T", markdown: strings.Repeat("body ", 100)})

	if _, found, err := uc.RefreshNext(ctx); err != nil || !found {
		t.Fatalf("refresh next: found=%v err=%v", found, err)
	}
	if got := idx.m[mine.Hash].URLChecked; !got.Equal(fixedClock{}.Now()) {
		t.Errorf("the attached source was preferred over the ordinary one")
	}
	if got := idx.m[attached.Hash].URLChecked; got.Equal(fixedClock{}.Now()) {
		t.Errorf("an attached snapshot was fetched")
	}
	if fetcher.calls != 1 {
		t.Errorf("fetched %d pages, want 1", fetcher.calls)
	}
}

// TestRefreshNextFindsADueFeedBehindAFullWindow: pages that are not due yet sort BEFORE a feed that
// is (a page checked two days ago is older than a feed checked yesterday, but only the feed is due).
// The window has to grow, or the feed starves until the pages reach their own week.
func TestRefreshNextFindsADueFeedBehindAFullWindow(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	for i := range 70 {
		seedURL(idx, "https://example.com/page-"+string(rune('a'+i%26))+string(rune('a'+i/26)), 2*24*time.Hour, "")
	}
	feed := seedURL(idx, "https://example.com/feed", 25*time.Hour, "")
	feed.RSS = true
	idx.m[feed.Hash] = feed
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	uc := pacedUC(t, idx, fetcher, &fakeConverter{title: "T", markdown: strings.Repeat("body ", 100)})

	res, found, err := uc.RefreshNext(ctx)
	if err != nil || !found {
		t.Fatalf("refresh next: found=%v err=%v", found, err)
	}
	if res.Checked != 1 {
		t.Fatalf("checked %d, want 1", res.Checked)
	}
	if got := idx.m[feed.Hash].URLChecked; !got.Equal(fixedClock{}.Now()) {
		t.Fatalf("the due feed was not found behind the not-yet-due pages")
	}
}

// TestRefreshNextReportsNothingDue: a paced worker that finds nothing waits; it must not report a
// failure, and it must not fetch.
func TestRefreshNextReportsNothingDue(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	seedURL(idx, "https://example.com/fresh", time.Minute, "")
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	uc := pacedUC(t, idx, fetcher, &fakeConverter{title: "T", markdown: strings.Repeat("body ", 100)})

	if _, found, err := uc.RefreshNext(ctx); err != nil || found {
		t.Fatalf("refresh next: found=%v err=%v, want nothing due", found, err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetched %d pages although nothing was due", fetcher.calls)
	}
}

// TestACancelledPassLeavesTheRestUntouched: a pass that runs out of time used to keep looping with a
// dead context — every remaining fetch failed at once and was recorded as a CHECK, which silences
// the source for a whole cadence.
func TestACancelledPassLeavesTheRestUntouched(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idx := newFakeIndex()
	// The fake lists from a map, so the order is the index's business, not this test's: the
	// assertion counts how many sources were touched, not which one came first.
	a := seedURL(idx, "https://example.com/a", 30*24*time.Hour, "")
	b := seedURL(idx, "https://example.com/b", 30*24*time.Hour, "")
	c := seedURL(idx, "https://example.com/c", 30*24*time.Hour, "")
	fetcher := &cancellingFetcher{
		fakeFetcher: &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}},
		cancel:      cancel,
	}
	uc := pacedUC(t, idx, fetcher, &fakeConverter{title: "T", markdown: strings.Repeat("body ", 100)})

	res, err := uc.RefreshAll(ctx)
	if err == nil {
		t.Fatalf("a cancelled pass reported success: %+v", res)
	}
	if res.Checked != 1 {
		t.Fatalf("a cancelled pass checked %d sources, want 1", res.Checked)
	}
	touched := 0
	for _, s := range []entities.Source{a, b, c} {
		if idx.m[s.Hash].URLChecked.Equal(fixedClock{}.Now()) {
			touched++
		}
	}
	if touched != 1 {
		t.Fatalf("%d sources were marked as checked after the cancellation, want 1", touched)
	}
}

// cancellingFetcher cancels the pass while it serves the first page, which is what a deadline does.
type cancellingFetcher struct {
	fakeFetcher *fakeFetcher
	cancel      context.CancelFunc
}

func (f *cancellingFetcher) Fetch(ctx context.Context, url string) (interfaces.FetchedPage, error) {
	if strings.HasSuffix(url, ".png") {
		return f.fakeFetcher.Fetch(ctx, url)
	}
	page, err := f.fakeFetcher.Fetch(ctx, url)
	f.cancel()
	return page, err
}
