package usecases_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

// fakeIndex is an in-memory IndexRepository that mimics the SQL upsert semantics:
// an existing hash keeps its row (id, title, ...).
type fakeIndex struct {
	m     map[string]entities.Source
	nodes map[ulid.ULID][]entities.SourceNode
	// steps is the mirror of the sources' .meta files, as the real repository holds it.
	steps map[string]entities.StepSet
	// pendingCompile is what SourcesPendingCompile answers (see the method).
	pendingCompile []entities.Source
}

func newFakeIndex() *fakeIndex {
	return &fakeIndex{
		m:     map[string]entities.Source{},
		nodes: map[ulid.ULID][]entities.SourceNode{},
		steps: map[string]entities.StepSet{},
	}
}

// UpsertSource mirrors the real query: a NEW hash is inserted whole, an existing one only has the
// derived-from-the-object fields refreshed (kind, ext, path, bytes) — the identity the index already
// carries is not touched by a reindex.
func (f *fakeIndex) UpsertSource(_ context.Context, s entities.Source) error {
	old, ok := f.m[s.Hash]
	if !ok {
		f.m[s.Hash] = s
		return nil
	}
	old.Kind, old.Ext, old.Path, old.Bytes = s.Kind, s.Ext, s.Path, s.Bytes
	// The real repository merges these identity markers from the entity into the row's meta.
	old.Sitemap, old.RSS = s.Sitemap, s.RSS
	old.FromFeed, old.FeedHash = s.FromFeed, s.FeedHash
	f.m[s.Hash] = old
	return nil
}

// FillURLSource is the fetch's write into a QUEUED url source: the identity stays, everything the
// fetch learned is written.
func (f *fakeIndex) FillURLSource(_ context.Context, s entities.Source) error {
	old, ok := f.m[s.Hash]
	if !ok {
		f.m[s.Hash] = s
		return nil
	}
	old.Kind, old.Ext, old.Path, old.Bytes = s.Kind, s.Ext, s.Path, s.Bytes
	old.Title, old.OriginalName, old.Bookmark, old.Meta = s.Title, s.OriginalName, s.Bookmark, s.Meta
	old.FromFeed, old.FeedHash = s.FromFeed, s.FeedHash
	old.RSS, old.Sitemap = s.RSS, s.Sitemap
	f.m[s.Hash] = old
	return nil
}

func (f *fakeIndex) GetSourceByHash(_ context.Context, hash string) (entities.Source, error) {
	s, ok := f.m[hash]
	if !ok {
		return entities.Source{}, types.New("not_found", "no such source", types.ErrNotFound)
	}
	return s, nil
}

// ListSourceChildren mirrors the two owner fields.
func (f *fakeIndex) ListSourceChildren(_ context.Context, parentHash, wikiPage string) ([]entities.Source, error) {
	out := make([]entities.Source, 0, 4)
	for _, src := range f.m {
		if (parentHash != "" && src.ParentHash == parentHash) || (wikiPage != "" && src.WikiPage == wikiPage) {
			out = append(out, src)
		}
	}
	return out, nil
}

func (f *fakeIndex) CountSourceChildren(ctx context.Context, parentHash, wikiPage string) (int, error) {
	srcs, err := f.ListSourceChildren(ctx, parentHash, wikiPage)
	return len(srcs), err
}

// CountSources mirrors the total behind a page.
func (f *fakeIndex) CountSources(_ context.Context, filter entities.SourceFilter) (int, error) {
	n := 0
	for _, s := range f.m {
		if filter.Kind == "" || string(s.Kind) == filter.Kind {
			n++
		}
	}
	return n, nil
}

// CountAttachedSources is not exercised by these use cases.
func (f *fakeIndex) CountAttachedSources(_ context.Context) (int, error) { return 0, nil }

func (f *fakeIndex) SetSourceTags(_ context.Context, hash string, tags []string) error {
	if src, ok := f.m[hash]; ok {
		src.Tags = tags
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceAutoTags(_ context.Context, hash string, tags []string, at time.Time) error {
	if src, ok := f.m[hash]; ok {
		src.AutoTags = tags
		src.AutoTagsAt = at
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceFavorite(_ context.Context, hash string, at time.Time) error {
	if src, ok := f.m[hash]; ok {
		src.FavoriteAt = at
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceReadLater(_ context.Context, hash string, at, doneAt time.Time, manual bool, via []string) error {
	if src, ok := f.m[hash]; ok {
		src.ReadLaterAt, src.ReadLaterDoneAt = at, doneAt
		src.ReadLaterManual, src.ReadLaterVia = manual, via
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SourceTagCounts(_ context.Context) (map[string]int, error) {
	out := map[string]int{}
	for _, src := range f.m {
		for _, tag := range src.Tags {
			out[tag]++
		}
	}
	return out, nil
}

func (f *fakeIndex) ListSources(_ context.Context, filter entities.SourceFilter, limit, offset int) ([]entities.Source, error) {
	hashes := make([]string, 0, len(f.m))
	for h := range f.m {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes) // a deterministic order, like the repository's ORDER BY
	var all []entities.Source
	for _, h := range hashes {
		if s := f.m[h]; filter.Kind == "" || string(s.Kind) == filter.Kind {
			all = append(all, s)
		}
	}
	if offset > len(all) {
		return nil, nil
	}
	all = all[offset:]
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// ListRecentSources mirrors the window: content sources newest first, bookmarks and containers
// counted but not listed.
func (f *fakeIndex) ListRecentSources(_ context.Context, since time.Time, limit, offset int) (interfaces.RecentSources, error) {
	var out interfaces.RecentSources
	if limit <= 0 {
		limit = 8
	}
	if offset < 0 {
		offset = 0
	}
	all := make([]entities.Source, 0, len(f.m))
	for _, s := range f.m {
		all = append(all, s)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ImportedAt.After(all[j].ImportedAt) })
	content := 0
	for _, s := range all {
		if !since.IsZero() && s.ImportedAt.Before(since) {
			continue
		}
		switch {
		case s.Bookmark:
			out.Bookmarks++
		case s.Sitemap || s.RSS:
			out.Containers++
		default:
			out.Total++
			if content >= offset && len(out.Sources) < limit {
				out.Sources = append(out.Sources, s)
			}
			content++
		}
	}
	return out, nil
}

func (f *fakeIndex) DeleteSourceByHash(_ context.Context, hash string) error {
	delete(f.m, hash)
	return nil
}

func (f *fakeIndex) SourceHashes(_ context.Context) ([]string, error) {
	out := make([]string, 0, len(f.m))
	for h := range f.m {
		out = append(out, h)
	}
	return out, nil
}

func (f *fakeIndex) SetSourceKindURL(_ context.Context, hash, url string) error {
	if src, ok := f.m[hash]; ok {
		src.Kind, src.URL = entities.SourceKindURL, url
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SourceKinds(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(f.m))
	for h, src := range f.m {
		out[h] = string(src.Kind)
	}
	return out, nil
}

func (f *fakeIndex) ResetSourceDerived(_ context.Context, hash, title string) error {
	if src, ok := f.m[hash]; ok {
		src.Title = title
		src.Pages = 0
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceLanguage(_ context.Context, hash, language string) error {
	if src, ok := f.m[hash]; ok {
		src.Language = language
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceOrigin(_ context.Context, hash, origin, wikiPage, parentHash string) error {
	if src, ok := f.m[hash]; ok {
		src.Origin, src.WikiPage, src.ParentHash = origin, wikiPage, parentHash
		f.m[hash] = src
	}
	return nil
}

// ClearSourceOrigin releases a source: the manual addition wins over its former owner.
func (f *fakeIndex) ClearSourceOrigin(_ context.Context, hash string) error {
	if src, ok := f.m[hash]; ok {
		src.Origin, src.WikiPage, src.ParentHash = "", "", ""
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SourceLanguages(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(f.m))
	for h, src := range f.m {
		if src.Language != "" {
			out[h] = src.Language
		}
	}
	return out, nil
}

func (f *fakeIndex) SetSourceTitle(_ context.Context, hash, title string) error {
	if strings.TrimSpace(title) == "" {
		return nil
	}
	if src, ok := f.m[hash]; ok {
		src.Title = title
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceHash(_ context.Context, oldHash, newHash, path string) error {
	src, ok := f.m[oldHash]
	if !ok {
		return nil
	}
	delete(f.m, oldHash)
	src.Hash, src.Path = newHash, path
	f.m[newHash] = src
	return nil
}

func (f *fakeIndex) SetSourceMeta(_ context.Context, hash, meta string) error {
	if meta == "" {
		return nil
	}
	if src, ok := f.m[hash]; ok {
		src.Meta = meta
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceSteps(_ context.Context, hash string, steps entities.StepSet) error {
	f.steps[hash] = steps
	if src, ok := f.m[hash]; ok {
		src.Steps = steps
		f.m[hash] = src
	}
	return nil
}

func (f *fakeIndex) SetSourceTree(_ context.Context, hash, state, title string, pages int) error {
	if s, ok := f.m[hash]; ok {
		s.TreeState = entities.TreeState(state)
		if title != "" {
			s.Title = title
		}
		if pages > 0 {
			s.Pages = pages
		}
		f.m[hash] = s
	}
	return nil
}

func (f *fakeIndex) ReplaceSourceNodes(_ context.Context, sourceID ulid.ULID, nodes []entities.SourceNode) error {
	f.nodes[sourceID] = nodes
	return nil
}

func (f *fakeIndex) ListSourceNodes(context.Context, ulid.ULID) ([]entities.SourceNode, error) {
	return nil, nil
}

func (f *fakeIndex) SourceHasAtoms(context.Context, string) (bool, error) {
	return false, nil
}

func (f *fakeIndex) SourcesWithOpenParts(context.Context) ([]entities.Source, error) {
	return nil, nil
}

func (f *fakeIndex) SourceOpenParts(context.Context, string) (int, error) { return 0, nil }

// SourcesWithUnclustered is what the real index derives from the memberships (a source whose
// claims no cluster owns yet). A fake cannot know that, so it is TOLD.
func (f *fakeIndex) SourcesWithUnclustered(context.Context) ([]entities.Source, error) {
	return f.pendingCompile, nil
}

func (f *fakeIndex) SourcesWithoutAtoms(context.Context) ([]entities.Source, error) {
	return nil, nil
}

func (f *fakeIndex) SourcesWithNoTree(context.Context) ([]entities.Source, error) {
	return nil, nil
}

func (f *fakeIndex) SetSourceURLState(_ context.Context, hash string, status int, checkedAt time.Time) error {
	if s, ok := f.m[hash]; ok {
		s.URLStatus = status
		s.URLChecked = checkedAt
		f.m[hash] = s
	}
	return nil
}

func (f *fakeIndex) ListURLSources(context.Context) ([]entities.Source, error) {
	var out []entities.Source
	for _, s := range f.m {
		if s.Kind == entities.SourceKindURL {
			out = append(out, s)
		}
	}
	return out, nil
}

func TestIntakeStoresAndDedupes(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	uc := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}

	first, err := uc.Execute(ctx, usecases.IntakeInput{Name: "note.md", Reader: strings.NewReader("# Hi")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	// STEP 1 DETERMINES THE TITLE: the bytes are readable as they are, so the document names itself
	// ("# Hi" is a heading) instead of waiting for the outline to happen.
	if first.Kind != "md" || first.Title != "Hi" {
		t.Errorf("unexpected: %+v", first)
	}

	second, err := uc.Execute(ctx, usecases.IntakeInput{Name: "copy.md", Reader: strings.NewReader("# Hi")})
	if err != nil {
		t.Fatalf("intake 2: %v", err)
	}
	if second.Hash != first.Hash {
		t.Errorf("hashes differ: %s vs %s", first.Hash, second.Hash)
	}
	if second.ID != first.ID {
		t.Errorf("dedupe lost the original id: %s vs %s", second.ID, first.ID)
	}
	if len(idx.m) != 1 {
		t.Errorf("index has %d rows, want 1", len(idx.m))
	}
}

func TestIntakeRejectsUnsupported(t *testing.T) {
	uc := usecases.IntakeSource{Store: objects.New(t.TempDir()), Index: newFakeIndex(), Clock: fixedClock{}}
	_, err := uc.Execute(context.Background(), usecases.IntakeInput{Name: "x.docx", Reader: strings.NewReader("x")})
	if !errors.Is(err, types.ErrUnsupportedSource) {
		t.Fatalf("err = %v, want ErrUnsupportedSource", err)
	}
}

func TestReindexAddsAndRemoves(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := objects.New(dir)
	idx := newFakeIndex()

	a, err := store.Put(ctx, ".md", strings.NewReader("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, ".txt", strings.NewReader("beta")); err != nil {
		t.Fatal(err)
	}

	uc := usecases.ReindexVault{Store: store, Index: idx, Clock: fixedClock{}}
	res, err := uc.Execute(ctx)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if res.Added != 2 || res.Indexed != 2 {
		t.Fatalf("res = %+v, want added=2 indexed=2", res)
	}

	// Remove one object and reindex: the row must go away.
	if err := os.Remove(filepath.Join(dir, a.Path)); err != nil {
		t.Fatal(err)
	}
	res, err = uc.Execute(ctx)
	if err != nil {
		t.Fatalf("reindex 2: %v", err)
	}
	if res.Removed != 1 || len(idx.m) != 1 {
		t.Fatalf("res = %+v rows=%d, want removed=1 rows=1", res, len(idx.m))
	}
}

// TestReindexKeepsAURLRecognizableAsAURL is the reason a source's IDENTITY lives in its record: the
// object of a URL snapshot is a plain <hash>.md — exactly like a Markdown source, and exactly like
// the readable rendering of a PDF. The file name therefore cannot say what a source IS, and a
// reindex that guessed from the extension would turn every URL into a Markdown source and lose the
// address it came from (and a bookmark would start being distilled, which the pipeline forbids).
func TestReindexKeepsAURLRecognizableAsAURL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := objects.New(dir)
	steps := sourcemeta.NewTracker(sourcemeta.New(dir), nil, nil)

	// A fetched page: stored as Markdown, recorded as what it is.
	urlObj, err := store.Put(ctx, ".md", strings.NewReader("# A fetched page\n"))
	if err != nil {
		t.Fatalf("put url snapshot: %v", err)
	}
	if err := steps.SetIdentity(urlObj.Hash, entities.SourceIdentity{
		Kind: entities.SourceKindURL, URL: "https://example.com/article", Bookmark: true,
	}); err != nil {
		t.Fatalf("record identity: %v", err)
	}
	// A Markdown source next to it: same extension, no record, and it IS Markdown.
	mdObj, err := store.Put(ctx, ".md", strings.NewReader("# A real Markdown source\n"))
	if err != nil {
		t.Fatalf("put md: %v", err)
	}

	idx := newFakeIndex()
	if _, err := (usecases.ReindexVault{Store: store, Index: idx, Steps: steps, Clock: fixedClock{}}).Execute(ctx); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	url, err := idx.GetSourceByHash(ctx, urlObj.Hash)
	if err != nil {
		t.Fatalf("url source missing: %v", err)
	}
	if url.Kind != entities.SourceKindURL {
		t.Errorf("kind = %q, want url — the record is the only thing that can say so", url.Kind)
	}
	if url.URL != "https://example.com/article" {
		t.Errorf("url = %q, want the address it was fetched from", url.URL)
	}
	if !url.Bookmark {
		t.Error("bookmark = false, want true — a collected link is never distilled")
	}
	md, err := idx.GetSourceByHash(ctx, mdObj.Hash)
	if err != nil {
		t.Fatalf("md source missing: %v", err)
	}
	if md.Kind != entities.SourceKindMD {
		t.Errorf("kind = %q, want md for a source without a record", md.Kind)
	}
}

// TestReindexKeepsTheKindTheIndexAlreadyKnows: a vault written before the identity was recorded has
// records without a kind. The index row still knows what its source is, and the file name does not —
// so the row wins, not the extension.
func TestReindexKeepsTheKindTheIndexAlreadyKnows(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	obj, err := store.Put(ctx, ".md", strings.NewReader("# fetched before the record carried a kind\n"))
	if err != nil {
		t.Fatal(err)
	}
	idx := newFakeIndex()
	idx.m[obj.Hash] = entities.Source{
		ID: ulid.Make(), Hash: obj.Hash, Kind: entities.SourceKindURL,
		Ext: obj.Ext, Path: obj.Path, URL: "https://example.com/old", ImportedAt: fixedClock{}.Now(),
	}

	if _, err := (usecases.ReindexVault{Store: store, Index: idx, Clock: fixedClock{}}).Execute(ctx); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	got, err := idx.GetSourceByHash(ctx, obj.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != entities.SourceKindURL {
		t.Errorf("kind = %q, want url (the index already knew; the file name cannot tell)", got.Kind)
	}
}
