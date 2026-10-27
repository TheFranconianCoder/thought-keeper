package usecases_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// fakeResetter records the derivation resets a replacement snapshot asks for.
type fakeResetter struct {
	calls []string
	err   error
}

func (r *fakeResetter) Execute(_ context.Context, hash string) (usecases.ResetResult, error) {
	r.calls = append(r.calls, hash)
	return usecases.ResetResult{}, r.err
}

func attachedSource(idx *fakeIndex, rawURL, path, from string) entities.Source {
	hash := entities.URLHash(rawURL)
	src := entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md", Path: path,
		URL: rawURL, Title: "Member only", OriginalName: rawURL, TreeState: entities.TreeNone,
		Attached: true, AttachedFrom: from, ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"` + rawURL + `","attached":true,"attached_from":"` + from + `"}`,
	}
	idx.m[hash] = src
	return src
}

func storedSnapshot(t *testing.T, store *objects.Store, hash string) string {
	t.Helper()
	rc, err := store.Open(hash, ".md")
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

// TestRefetchReplacesAnAttachedSnapshot: the explicit "try the URL again" is the way out of an
// attachment. A readable page replaces the snapshot, clears the marker and resets the derivation
// that belonged to the attached bytes.
func TestRefetchReplacesAnAttachedSnapshot(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	log := &fakeSourceLog{}
	reset := &fakeResetter{}
	src := attachedSource(idx, "https://example.com/member", "sources/aa/bb/member.md", "page.html")
	if _, err := store.PutAt(src.Hash, ".md", []byte("---\nattached: true\n---\n\nold attached text")); err != nil {
		t.Fatalf("store old snapshot: %v", err)
	}

	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>fresh</html>")}}
	conv := &fakeConverter{title: "The Member Article", markdown: strings.Repeat("fresh readable text ", 40)}
	refresh := usecases.RefreshURLs{
		Fetch: fetcher, Convert: conv, Store: store, Index: idx, Steps: steps,
		SourceLog: log, Reset: reset, Clock: fixedClock{},
	}

	res, err := refresh.ExecuteOneForce(ctx, src.Hash)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("the URL was fetched %d time(s), want 1", fetcher.calls)
	}
	if res.Replaced != 1 || res.Changed != 1 || res.Blocked != 0 {
		t.Fatalf("result = %+v, want one replaced and changed source", res)
	}
	got := idx.m[src.Hash]
	if strings.Contains(got.Meta, "attached") {
		t.Errorf("the attachment marker survived: %s", got.Meta)
	}
	if len(reset.calls) != 1 || reset.calls[0] != src.Hash {
		t.Errorf("derivation reset calls = %v, want one for the source", reset.calls)
	}
	if id := steps.identities[src.Hash]; id.Attached || id.AttachedFrom != "" {
		t.Errorf("identity still carries the attachment: %+v", id)
	}
	snapshot := storedSnapshot(t, store, src.Hash)
	if strings.Contains(snapshot, "attached: true") || !strings.Contains(snapshot, "fresh readable text") {
		t.Errorf("snapshot = %q, want the fetched page without the attachment receipt", snapshot)
	}
	if !log.contains("attached snapshot replaced by a fetch") {
		t.Errorf("the source log does not record the replacement: %s", log.joined())
	}
}

// TestRefetchKeepsAnAttachedSnapshotWhenThePageIsStillWalled: the fetch may only replace the human's
// copy; a wall leaves it (and its marker) exactly where it was.
func TestRefetchKeepsAnAttachedSnapshotWhenThePageIsStillWalled(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	reset := &fakeResetter{}
	src := attachedSource(idx, "https://example.com/member", "sources/aa/bb/member.md", "page.html")
	old := "---\nattached: true\n---\n\nold attached text"
	if _, err := store.PutAt(src.Hash, ".md", []byte(old)); err != nil {
		t.Fatalf("store old snapshot: %v", err)
	}

	fetcher := &fakeFetcher{page: interfaces.FetchedPage{Status: 403, Wall: entities.WallLogin}}
	refresh := usecases.RefreshURLs{
		Fetch: fetcher, Convert: &fakeConverter{}, Store: store, Index: idx, Steps: steps,
		Reset: reset, Clock: fixedClock{},
	}

	res, err := refresh.ExecuteOneForce(ctx, src.Hash)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("the URL was fetched %d time(s), want 1", fetcher.calls)
	}
	if res.Blocked != 1 || res.Replaced != 0 || res.Changed != 0 {
		t.Fatalf("result = %+v, want a blocked attempt that replaced nothing", res)
	}
	if got := storedSnapshot(t, store, src.Hash); got != old {
		t.Errorf("the attached snapshot changed on a wall:\n%s", got)
	}
	if got := idx.m[src.Hash].Meta; !strings.Contains(got, `"attached":true`) {
		t.Errorf("the attachment marker was lost on a wall: %s", got)
	}
	if len(reset.calls) != 0 {
		t.Errorf("a blocked fetch reset the derivation: %v", reset.calls)
	}
}

// TestAttachReplacesADerivedSnapshot: replacing the file is a new source text, so the derivation the
// old bytes produced is reset before the new snapshot is written.
func TestAttachReplacesADerivedSnapshot(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	reset := &fakeResetter{}
	src := attachedSource(idx, "https://example.com/member", "sources/aa/bb/member.md", "old.html")

	uc := usecases.AttachURLSnapshot{
		Convert: &fakeConverter{title: "The New Page", markdown: strings.Repeat("new readable text ", 40)},
		Store:   store, Index: idx, Reset: reset, Clock: fixedClock{},
	}
	if _, err := uc.Execute(ctx, usecases.AttachInput{
		Hash: src.Hash, Name: "new.html", Data: []byte("<html>new</html>"),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(reset.calls) != 1 || reset.calls[0] != src.Hash {
		t.Fatalf("the old derivation was not reset: %v", reset.calls)
	}
	snapshot := storedSnapshot(t, store, src.Hash)
	if !strings.Contains(snapshot, "attached_from: new.html") {
		t.Errorf("snapshot still names the old file:\n%s", snapshot)
	}
}
