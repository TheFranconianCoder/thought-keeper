package usecases_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// urlSourceRow puts a URL source in the index: the row a walled fetch leaves behind, or one a wiki
// page claimed. It is the source an attached snapshot fills.
func urlSourceRow(idx *fakeIndex, rawURL string) entities.Source {
	hash := entities.URLHash(rawURL)
	src := entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md", URL: rawURL,
		Title: rawURL, OriginalName: rawURL, TreeState: entities.TreeNone, ImportedAt: fixedClock{}.Now(),
		Meta: "{}",
	}
	idx.m[hash] = src
	return src
}

func readObject(t *testing.T, uc usecases.AttachURLSnapshot, hash string) string {
	t.Helper()
	rc, err := uc.Store.Open(hash, ".md")
	if err != nil {
		t.Fatalf("the snapshot is missing: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return string(b)
}

// TestAttachURLSnapshotTakesTheHTML: the human saved the page, so the app reads it — the URL stays the
// address, the file becomes the snapshot, and the document steps are re-armed.
func TestAttachURLSnapshotTakesTheHTML(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	log := &fakeSourceLog{}
	src := urlSourceRow(idx, "https://example.com/member-article")

	uc := usecases.AttachURLSnapshot{
		Convert: &fakeConverter{
			title:    "The Member Article",
			markdown: "## A section\n\n" + strings.Repeat("body text ", 40),
		},
		Store: store, Index: idx, Steps: steps, SourceLog: log, Clock: fixedClock{},
	}
	out, err := uc.Execute(ctx, usecases.AttachInput{
		Hash: src.Hash, Name: "member-article.html", Data: []byte("<html>saved page</html>"),
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if out.Hash != src.Hash {
		t.Errorf("attach moved the source: %s vs %s", out.Hash, src.Hash)
	}
	if out.Title != "The Member Article" || out.Bookmark {
		t.Errorf("row = %+v, want the page's title and no bookmark", out)
	}

	snapshot := readObject(t, uc, src.Hash)
	for _, want := range []string{
		"url: https://example.com/member-article",
		"attached_from: member-article.html",
		"attached: true",
		"## A section",
	} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, snapshot)
		}
	}
	if strings.Contains(snapshot, "http_status") {
		t.Errorf("an attached snapshot must not claim an HTTP answer:\n%s", snapshot)
	}

	if !strings.Contains(idx.m[src.Hash].Meta, `"attached":true`) {
		t.Errorf("the record does not say it was attached: %s", idx.m[src.Hash].Meta)
	}
	if len(steps.resets) == 0 {
		t.Error("the old derivation was not reset")
	}
	if got := lastState(steps, entities.StepCapture); got != entities.StepOK {
		t.Errorf("capture = %q, want ok", got)
	}
	id := steps.identities[src.Hash]
	if !id.Attached || id.AttachedFrom != "member-article.html" || id.AttachedHash == "" {
		t.Errorf("identity lost the attachment: %+v", id)
	}
	if !log.contains("snapshot attached") {
		t.Errorf("the source log does not say it: %s", log.joined())
	}
}

// TestAttachURLSnapshotTakesThePDF: a browser's "print to PDF" is the other way a human hands over a
// page, and it goes through the extractor the rest of the pipeline uses.
func TestAttachURLSnapshotTakesThePDF(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	src := urlSourceRow(idx, "https://example.com/report")

	uc := usecases.AttachURLSnapshot{
		Extractor: &fakeExtractor{parts: []entities.Part{
			{Level: 1, Title: "Chapter One", Text: "It begins here."},
			{Level: 2, Title: "A detail", Text: "More prose."},
		}},
		Store: store, Index: idx, Clock: fixedClock{},
	}
	out, err := uc.Execute(ctx, usecases.AttachInput{
		Hash: src.Hash, Name: "The Report.pdf", Data: []byte("%PDF-1.7"),
	})
	if err != nil {
		t.Fatalf("attach pdf: %v", err)
	}
	if out.Title != "The Report" {
		t.Errorf("title = %q, want the file's own name", out.Title)
	}
	snapshot := readObject(t, uc, src.Hash)
	for _, want := range []string{"# Chapter One", "It begins here.", "## A detail", "More prose."} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, snapshot)
		}
	}
	if strings.Contains(snapshot, "<!--part") {
		t.Errorf("a rendering marker leaked into a Markdown snapshot:\n%s", snapshot)
	}
}

func TestAttachURLSnapshotRejectsWhatItCannotRead(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	src := urlSourceRow(idx, "https://example.com/a")
	uc := usecases.AttachURLSnapshot{Store: store, Index: idx, Clock: fixedClock{}}

	tests := []struct {
		name string
		in   usecases.AttachInput
	}{
		{name: "unknown source", in: usecases.AttachInput{Hash: entities.URLHash("https://example.com/none"), Name: "a.html", Data: []byte("x")}},
		{name: "empty file", in: usecases.AttachInput{Hash: src.Hash, Name: "a.html", Data: nil}},
		{name: "unsupported extension", in: usecases.AttachInput{Hash: src.Hash, Name: "a.docx", Data: []byte("x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := uc.Execute(ctx, tt.in); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// TestAttachURLSnapshotRefusesANonURL: only a URL source has an address a human can attach to. A
// Markdown file is its own bytes — attaching a page to it would invent a provenance.
func TestAttachURLSnapshotRefusesANonURL(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	md, err := objects.New(t.TempDir()).Put(ctx, ".md", strings.NewReader("# A note\n"))
	if err != nil {
		t.Fatal(err)
	}
	idx.m[md.Hash] = entities.Source{
		ID: ulid.Make(), Hash: md.Hash, Kind: entities.SourceKindMD, Ext: md.Ext, Path: md.Path,
		Title: "A note", ImportedAt: fixedClock{}.Now(), Meta: "{}",
	}
	uc := usecases.AttachURLSnapshot{Store: objects.New(t.TempDir()), Index: idx, Clock: fixedClock{}}
	if _, err := uc.Execute(ctx, usecases.AttachInput{Hash: md.Hash, Name: "a.html", Data: []byte("<html>x</html>")}); err == nil {
		t.Fatal("a Markdown source took an attached snapshot")
	}
}

// TestAttachURLSnapshotTakesABookmark: a page that yielded too little text (a JavaScript app, a
// video) is a bookmark — and the human's saved copy must be able to fill it, or such a page could
// never become readable.
func TestAttachURLSnapshotTakesABookmark(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	src := urlSourceRow(idx, "https://example.com/js-page")
	src.Bookmark = true
	src.Path = "sources/aa/bb/" + src.Hash + ".md"
	src.Meta = `{"url":"https://example.com/js-page","bookmark":true}`
	if _, err := store.PutAt(src.Hash, ".md", []byte("a bookmark note\n")); err != nil {
		t.Fatal(err)
	}
	idx.m[src.Hash] = src

	uc := usecases.AttachURLSnapshot{
		Convert: &fakeConverter{title: "The Page", markdown: strings.Repeat("the article ", 40)},
		Store:   store, Index: idx, Steps: steps, Clock: fixedClock{},
	}
	if _, err := uc.Execute(ctx, usecases.AttachInput{
		Hash: src.Hash, Name: "page.html", Data: []byte("<html>saved</html>"),
	}); err != nil {
		t.Fatalf("attach to a bookmark: %v", err)
	}
	if idx.m[src.Hash].Bookmark {
		t.Error("the attached source is still a bookmark")
	}
	if len(steps.resets) == 0 {
		t.Error("the bookmark's derivation was not re-armed")
	}
}

// TestAttachURLSnapshotRefusesAFeed: a feed's snapshot is its item list, not a page. Attaching to it
// would replace the list with one article.
func TestAttachURLSnapshotRefusesAFeed(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	src := urlSourceRow(idx, "https://example.com/feed.xml")
	src.RSS = true
	idx.m[src.Hash] = src
	uc := usecases.AttachURLSnapshot{Store: objects.New(t.TempDir()), Index: idx, Clock: fixedClock{}}
	if _, err := uc.Execute(ctx, usecases.AttachInput{Hash: src.Hash, Name: "page.html", Data: []byte("<html>x</html>")}); err == nil {
		t.Fatal("a feed took an attached snapshot")
	}
}
