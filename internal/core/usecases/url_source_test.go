package usecases_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/favicons"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

type fakeFetcher struct {
	page interfaces.FetchedPage
	err  error
	// failIcon makes the ICON request fail while the page itself answers: the transient case that used
	// to lose a source's icon on every refresh of a changed page.
	failIcon bool
	// calls counts the page requests, so a test can prove a source was NOT fetched (an attached
	// snapshot).
	calls int
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) (interfaces.FetchedPage, error) {
	if strings.HasSuffix(url, ".png") {
		if f.failIcon {
			return interfaces.FetchedPage{}, errors.New("icon not reachable")
		}
		return interfaces.FetchedPage{Status: 200, Body: []byte("PNGDATA"), ContentType: "image/png"}, nil
	}
	f.calls++
	return f.page, f.err
}

type fakeConverter struct {
	title    string
	markdown string
	favicon  string
}

func (f *fakeConverter) Convert(context.Context, []byte, string) (interfaces.ConvertedPage, error) {
	return interfaces.ConvertedPage{Title: f.title, Markdown: f.markdown, Favicon: f.favicon}, nil
}

func TestAddURLStoresLocalSnapshot(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	uc := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>…</html>")}},
		Convert: &fakeConverter{title: "The Page", markdown: "# Heading\n\nBody text."},
		Store:   objects.New(t.TempDir()),
		Index:   idx,
		Clock:   fixedClock{},
	}

	src, err := uc.Execute(ctx, "https://example.com/article", false)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}
	if src.Kind != "url" || src.URL != "https://example.com/article" {
		t.Errorf("unexpected source: %+v", src)
	}
	if src.URLStatus != 200 || src.URLChecked.IsZero() {
		t.Errorf("url state not recorded: status=%d checked=%v", src.URLStatus, src.URLChecked)
	}
	if src.Title != "The Page" {
		t.Errorf("title = %q", src.Title)
	}
}

func TestAddURLPersistsFaviconAndContentHashInTheRecord(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	icons := favicons.New(t.TempDir())
	steps := &fakeSteps{}
	uc := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>…</html>")}},
		Convert: &fakeConverter{title: "The Page", markdown: strings.Repeat("body text ", 60), favicon: "/icon.png"},
		Store:   store, Index: idx, Favicons: icons, Steps: steps, Clock: fixedClock{},
	}
	src, err := uc.Execute(ctx, "https://example.com/article", false)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}
	id := steps.identities[src.Hash]
	if id.ContentHash == "" || id.FaviconHash == "" || id.FaviconExt == "" {
		t.Fatalf("identity lost the url metadata: %+v", id)
	}
}

func TestRefreshURLsDetectsChange(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	// long enough to be an indexed document (not a bookmark)
	conv := &fakeConverter{title: "T", markdown: strings.Repeat("same text ", 60)}

	add := usecases.AddURL{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{}}
	if _, err := add.Execute(ctx, "https://example.com/x", false); err != nil {
		t.Fatalf("add: %v", err)
	}

	refresh := usecases.RefreshURLs{
		Atoms: nil, Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{},
	}

	res, err := refresh.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Checked != 1 || res.Changed != 0 {
		t.Fatalf("unchanged refresh = %+v, want checked 1 changed 0", res)
	}

	conv.markdown = strings.Repeat("same text ", 60) + " but the page changed"
	res, err = refresh.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh 2: %v", err)
	}
	if res.Changed != 1 || len(res.Sources) != 1 {
		t.Fatalf("changed refresh = %+v, want changed 1", res)
	}
	if !strings.Contains(res.Sources[0].Meta, "content_hash") {
		t.Errorf("new snapshot lacks content hash meta: %s", res.Sources[0].Meta)
	}
}

func TestAddURLStoresBookmarkForUnreadablePage(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	uc := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>video</html>")}},
		Convert: &fakeConverter{title: "A Video", markdown: "Subscribe to the channel."},
		Store:   objects.New(t.TempDir()),
		Index:   idx,
		Clock:   fixedClock{},
	}

	// Too little text to index → a collected link.
	src, err := uc.Execute(ctx, "https://video.example/watch?v=abc", false)
	if err != nil {
		t.Fatalf("add bookmark: %v", err)
	}
	if !src.Bookmark {
		t.Fatalf("short page should be a bookmark, got %+v", src)
	}
	if !strings.Contains(src.Meta, `"bookmark":true`) {
		t.Errorf("meta lacks the bookmark flag: %s", src.Meta)
	}

	// Forced bookmark wins even when the page has plenty of text.
	long := &usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}},
		Convert: &fakeConverter{title: "Article", markdown: strings.Repeat("word ", 200)},
		Store:   objects.New(t.TempDir()),
		Index:   newFakeIndex(),
		Clock:   fixedClock{},
	}
	forced, err := long.Execute(ctx, "https://example.com/article", true)
	if err != nil {
		t.Fatalf("forced bookmark: %v", err)
	}
	if !forced.Bookmark {
		t.Errorf("forced bookmark was indexed instead: %+v", forced)
	}
	// …and without the flag the same page is an indexed document.
	indexed, err := long.Execute(ctx, "https://example.com/article2", false)
	if err != nil {
		t.Fatalf("indexed add: %v", err)
	}
	if indexed.Bookmark {
		t.Errorf("long article should be indexed, not a bookmark: %+v", indexed)
	}
}

// TestAddingAURLTwiceRefreshesItInPlace: `capture.dedupe_by_url` asks for one source per URL. Adding
// a URL that is already in the vault — or adding it again after the page changed — must write through
// the source that is there, not file a second one beside it. A duplicate is not a cosmetic problem: a
// second snapshot means a second, from-scratch derivation of the same document.
func TestAddingAURLTwiceRefreshesItInPlace(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	conv := &fakeConverter{title: "T", markdown: strings.Repeat("same text ", 60)}
	add := usecases.AddURL{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{}}

	first, err := add.Execute(ctx, "https://example.com/x", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// The same URL again, unchanged: still one source.
	if _, err := add.Execute(ctx, "https://example.com/x", false); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if all := sourcesByURL(t, idx, "https://example.com/x"); len(all) != 1 {
		t.Fatalf("re-adding an unchanged URL filed %d sources, want 1", len(all))
	}
	// And again after the page changed: the same source, the same address, the new bytes.
	conv.markdown = strings.Repeat("same text ", 60) + " and now it changed"
	again, err := add.Execute(ctx, "https://example.com/x", false)
	if err != nil {
		t.Fatalf("re-add changed: %v", err)
	}
	all := sourcesByURL(t, idx, "https://example.com/x")
	if len(all) != 1 {
		t.Fatalf("re-adding a changed URL filed %d sources, want 1", len(all))
	}
	if again.Hash != first.Hash {
		t.Errorf("the address changed (%s → %s): a URL source keeps the one it was created with",
			first.Hash, again.Hash)
	}
	// The snapshot really is the new one.
	rc, err := store.Open(first.Hash, ".md")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	if !strings.Contains(string(b), "and now it changed") {
		t.Errorf("the snapshot was not refreshed in place: %s", b)
	}
}

func TestRefreshRepairsAMissingFaviconOnAnUnchangedPage(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	icons := favicons.New(t.TempDir())
	steps := &fakeSteps{}
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("x")}}
	conv := &fakeConverter{title: "T", markdown: strings.Repeat("same text ", 60), favicon: "/icon.png"}
	add := usecases.AddURL{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Favicons: icons, Steps: steps, Clock: fixedClock{}}
	src, err := add.Execute(ctx, "https://example.com/repair", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Simulate the old bug: the index/record carry no icon, but the content hash is still known.
	row := idx.m[src.Hash]
	row.Meta = `{"content_hash":"` + contentHashForTest("T", strings.Repeat("same text ", 60)) + `","url":"https://example.com/repair"}`
	idx.m[src.Hash] = row
	delete(steps.identities, src.Hash)

	refresh := usecases.RefreshURLs{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Favicons: icons, Steps: steps, Clock: fixedClock{}}
	res, err := refresh.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Changed != 0 {
		t.Fatalf("unchanged page was reported changed: %+v", res)
	}
	got := idx.m[src.Hash]
	if !strings.Contains(got.Meta, `"favicon"`) {
		t.Fatalf("missing favicon was not repaired: %s", got.Meta)
	}
	if id := steps.identities[src.Hash]; id.FaviconHash == "" || id.ContentHash == "" {
		t.Fatalf("identity was not repaired: %+v", id)
	}
}

func contentHashForTest(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\n\n" + strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:])
}

// The icon belongs to the SITE, not to one snapshot: a refresh that changes the page must not lose it.
// It used to: the new snapshot's meta was written without a favicon, so a page that changes regularly
// lost its icon at the first refresh — and the icon looked "gone" without anyone deleting anything.
//
// A changed page is refreshed IN PLACE — the same source, the same address — so this also guards the
// other half of the same mistake: a refresh must not file a second snapshot beside the first.
func TestRefreshKeepsTheFaviconOfAChangedPage(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	icons := favicons.New(t.TempDir())
	page := interfaces.FetchedPage{Status: 200, Body: []byte("x")}
	fetcher := &fakeFetcher{page: page}
	conv := &fakeConverter{title: "T", markdown: strings.Repeat("same text ", 60), favicon: "/icon.png"}

	add := usecases.AddURL{
		Fetch: fetcher, Convert: conv, Store: store, Index: idx, Favicons: icons, Clock: fixedClock{},
	}
	if _, err := add.Execute(ctx, "https://example.com/x", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	first := sourcesByURL(t, idx, "https://example.com/x")[0]
	if !strings.Contains(first.Meta, `"favicon"`) {
		t.Fatalf("the intake stored no icon: %s", first.Meta)
	}

	// The site's icon cannot be fetched any more (a transient failure) AND the page changed.
	fetcher.failIcon = true
	conv.markdown = strings.Repeat("same text ", 60) + " but the page changed"
	refresh := usecases.RefreshURLs{
		Fetch: fetcher, Convert: conv, Store: store, Index: idx, Favicons: icons, Clock: fixedClock{},
	}
	res, err := refresh.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Changed != 1 {
		t.Fatalf("refresh = %+v, want the changed page", res)
	}
	all := sourcesByURL(t, idx, "https://example.com/x")
	if len(all) != 1 {
		t.Fatalf("a refresh must update the source in place, got %d sources for one URL", len(all))
	}
	if !strings.Contains(all[0].Meta, `"favicon"`) {
		t.Errorf("the refreshed snapshot lost the icon: %s", all[0].Meta)
	}
}

// sourcesByURL is every source the index holds for a URL: a refresh of a CHANGED page adds a snapshot
// (the old one stays, its claims are what gets superseded), so there can be more than one.
func sourcesByURL(t *testing.T, idx *fakeIndex, url string) []entities.Source {
	t.Helper()
	var out []entities.Source
	for _, s := range idx.m {
		if s.URL == url {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no source for %s", url)
	}
	return out
}

// TestAddURLRecordsALoginWall: a page that answers with a gate is a RESULT, not a transport failure.
// The row exists, the capture light is red with the reason, and the two later steps are skipped — the
// outline must never build a tree out of a login form and the distill step must never write claims
// about it.
func TestAddURLRecordsALoginWall(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	log := &fakeSourceLog{}
	uc := usecases.AddURL{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 401, Wall: entities.WallLogin, FinalURL: "https://example.com/login",
		}},
		Convert: &fakeConverter{title: "Sign in", markdown: "Please sign in."},
		Store:   store, Index: idx, Steps: steps, SourceLog: log, Clock: fixedClock{},
	}

	src, err := uc.Execute(ctx, "https://example.com/private", false)
	if err != nil {
		t.Fatalf("a wall must be recorded, not returned as an error: %v", err)
	}
	row := idx.m[src.Hash]
	if !strings.Contains(row.Meta, `"wall":"login_required"`) {
		t.Errorf("the wall is not in the record: %s", row.Meta)
	}
	if strings.Contains(row.Meta, "content_hash") {
		t.Errorf("a walled page must not claim a content hash: %s", row.Meta)
	}
	if row.Bookmark {
		t.Errorf("a wall is not a bookmark: %+v", row)
	}
	if got := lastState(steps, entities.StepCapture); got != entities.StepFailed {
		t.Errorf("capture = %q, want failed", got)
	}
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
		if got := lastState(steps, step); got != entities.StepSkipped {
			t.Errorf("%s = %q, want skipped", step, got)
		}
	}
	rc, err := store.Open(src.Hash, ".md")
	if err != nil {
		t.Fatalf("the wall snapshot is missing: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "wall: login_required") {
		t.Errorf("the snapshot does not say what happened:\n%s", body)
	}
	if !strings.Contains(string(body), "attach what you can see") {
		t.Errorf("the snapshot does not say what to do:\n%s", body)
	}
}

// TestAddURLKeepsAReadableSnapshotWhenThePageWalls: the wall describes the FETCH, not the bytes. A page
// that was readable once keeps the text we were allowed to read — replacing it with a login form would
// throw away knowledge to report a failed check.
func TestAddURLKeepsAReadableSnapshotWhenThePageWalls(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200}}
	conv := &fakeConverter{title: "The Article", markdown: strings.Repeat("the article text ", 30)}
	add := usecases.AddURL{Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{}}
	first, err := add.Execute(ctx, "https://example.com/a", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	rc, _ := store.Open(first.Hash, ".md")
	before, _ := io.ReadAll(rc)
	_ = rc.Close()

	fetcher.page = interfaces.FetchedPage{Status: 401, Wall: entities.WallLogin}
	second, err := add.Execute(ctx, "https://example.com/a", false)
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	rc, _ = store.Open(second.Hash, ".md")
	after, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(before) != string(after) {
		t.Error("the readable snapshot was replaced by the login page")
	}
	if !strings.Contains(idx.m[second.Hash].Meta, `"wall":"login_required"`) {
		t.Errorf("the wall is not recorded: %s", idx.m[second.Hash].Meta)
	}
}

// TestRefreshLeavesAnAttachedSnapshotAlone: an attached snapshot exists BECAUSE the page cannot be
// fetched. Asking the site again would only write a 403 onto an article that is fine, so the source is
// not fetched at all.
func TestRefreshLeavesAnAttachedSnapshotAlone(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	hash := entities.URLHash("https://example.com/member-only")
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + hash + ".md", URL: "https://example.com/member-only",
		Title: "Member only", OriginalName: "https://example.com/member-only",
		Meta:       `{"url":"https://example.com/member-only","attached":true,"attached_from":"page.html"}`,
		ImportedAt: fixedClock{}.Now(),
	}
	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200}}
	refresh := usecases.RefreshURLs{Fetch: fetcher, Convert: &fakeConverter{}, Store: store, Index: idx, Clock: fixedClock{}}

	res, err := refresh.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if fetcher.calls != 0 {
		t.Errorf("an attached source was fetched %d time(s)", fetcher.calls)
	}
	if res.Attached != 1 || res.Checked != 0 {
		t.Errorf("res = %+v, want attached 1 checked 0", res)
	}
}

// lastState is the state a step was left in, from the recorded transitions.
func lastState(steps *fakeSteps, step entities.Step) entities.StepState {
	seen := steps.seen[step]
	if len(seen) == 0 {
		return ""
	}
	return seen[len(seen)-1].State
}

// TestRefreshOnlyChecksDueURLs: the cadence belongs to the SOURCE, not to the process. With a plain
// ticker, an app that is restarted daily would never reach its weekly refresh at all.
func TestRefreshOnlyChecksDueURLs(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	now := fixedClock{}.Now()

	recent := entities.URLHash("https://example.com/recent")
	idx.m[recent] = entities.Source{
		ID: ulid.Make(), Hash: recent, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + recent + ".md", URL: "https://example.com/recent",
		Title: "Recent", ImportedAt: now, URLChecked: now, Meta: "{}",
	}
	stale := entities.URLHash("https://example.com/stale")
	idx.m[stale] = entities.Source{
		ID: ulid.Make(), Hash: stale, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/cc/dd/" + stale + ".md", URL: "https://example.com/stale",
		Title: "Stale", ImportedAt: now, URLChecked: now.Add(-8 * 24 * time.Hour), Meta: "{}",
	}

	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200}}
	conv := &fakeConverter{title: "T", markdown: strings.Repeat("same text ", 60)}
	scheduled := usecases.RefreshURLs{
		Fetch: fetcher, Convert: conv, Store: store, Index: idx, Clock: fixedClock{},
		MinAge: 7 * 24 * time.Hour,
	}
	res, err := scheduled.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Checked != 1 || fetcher.calls != 1 {
		t.Errorf("checked = %d (calls %d), want only the stale URL", res.Checked, fetcher.calls)
	}

	// The manual pass ignores the cadence: the button means "check now".
	res, err = scheduled.ExecuteAll(ctx)
	if err != nil {
		t.Fatalf("refresh all: %v", err)
	}
	if res.Checked != 2 || fetcher.calls != 3 {
		t.Errorf("checked = %d (calls %d), want both URLs", res.Checked, fetcher.calls)
	}
}

// TestWalledQueuedLinkKeepsItsOwner: a URL a wiki page queued keeps the page that owns it when the
// fetch answers with a wall. The ownership lives only in the row's meta JSON, and the wall write
// replaces that JSON — if it does not carry `origin`/`wiki_page`, the source page shows no owner and
// removal safety has no answer.
func TestWalledQueuedLinkKeepsItsOwner(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	const pageID = "01M5JAHSRBDSX83F8V6CR3DRH3"
	const raw = "https://example.com/linked"
	if _, err := (usecases.QueueURLs{Index: idx, Steps: steps, Clock: fixedClock{}}).Execute(ctx, pageID, []string{raw}); err != nil {
		t.Fatalf("queue: %v", err)
	}

	add := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 401, Wall: entities.WallLogin}},
		Convert: &fakeConverter{title: "Sign in", markdown: "gate"},
		Store:   store, Index: idx, Steps: steps, Clock: fixedClock{},
	}
	src, err := add.Execute(ctx, raw, false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	meta := idx.m[src.Hash].Meta
	if !strings.Contains(meta, `"origin":"wiki"`) {
		t.Errorf("the walled link lost its owner: %s", meta)
	}
	if !strings.Contains(meta, `"wiki_page":"`+pageID+`"`) {
		t.Errorf("the walled link lost its wiki page: %s", meta)
	}
}

// TestReconcileRestoresAnOwnerTheRowLost: the record is the truth and the index is a mirror. A row
// written before the snapshot merge existed lost origin/wiki_page while its record kept them; the
// consistency job copies them back instead of leaving a source with no visible owner. One pass heals
// EVERY drifted row — the check is vault-wide, so no source has to be opened or derived again.
func TestReconcileRestoresAnOwnerTheRowLost(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	const pageID = "01M5JAHSRBDSX83F8V6CR3DRH3"
	steps.identities = map[string]entities.SourceIdentity{}

	damaged := func(rawURL string) string {
		hash := entities.URLHash(rawURL)
		idx.m[hash] = entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
			Path: "sources/aa/bb/" + hash + ".md", URL: rawURL,
			Title: "Linked", Bookmark: true, ImportedAt: fixedClock{}.Now(),
			Meta: `{"url":"` + rawURL + `"}`,
		}
		steps.identities[hash] = entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: rawURL, Bookmark: true,
			Origin: "wiki", WikiPage: pageID,
		}
		return hash
	}
	first := damaged("https://example.com/one")
	second := damaged("https://example.com/two")

	reconcile := usecases.ReconcileVault{
		Index: idx, Steps: steps, Store: store, SourceLog: &fakeSourceLog{}, Clock: fixedClock{},
	}
	// The read-only half counts the repair (that is what makes the app run the real pass) ...
	dry := reconcile
	dry.DryRun = true
	res, err := dry.Execute(ctx)
	if err != nil {
		t.Fatalf("dry reconcile: %v", err)
	}
	if res.Ownerships != 2 {
		t.Fatalf("dry pass = %+v, want two ownership repairs", res)
	}
	if idx.m[first].Origin != "" || idx.m[second].Origin != "" {
		t.Error("the dry pass wrote")
	}

	// ... and one real pass writes all of them.
	res, err = reconcile.Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Ownerships != 2 {
		t.Errorf("real pass = %+v, want two ownership repairs", res)
	}
	for _, hash := range []string{first, second} {
		row := idx.m[hash]
		if row.Origin != "wiki" || row.WikiPage != pageID {
			t.Errorf("row %s owner = %q/%q, want the record's wiki page", hash[:8], row.Origin, row.WikiPage)
		}
	}
}
