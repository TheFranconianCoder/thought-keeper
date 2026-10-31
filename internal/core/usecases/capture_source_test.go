package usecases_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// fakeExtractor stands in for poppler/OCR: it returns scripted parts, so a capture can be tested
// without the tools (the same boundary the extract package is tested through).
type fakeExtractor struct {
	parts    []entities.Part
	title    string
	language string
	err      error
	calls    int
}

func (f *fakeExtractor) Extract(_ context.Context, _ string, _ []byte, onEvent func(entities.ExtractEvent)) (entities.Extraction, error) {
	f.calls++
	if onEvent != nil {
		onEvent(entities.ExtractEvent{Kind: "furniture", Lines: 3, Pages: 2})
	}
	if f.err != nil {
		return entities.Extraction{}, f.err
	}
	return entities.Extraction{Parts: f.parts, Title: f.title, Language: f.language}, nil
}

// explodingExtractor fails the test when it is used at all: the outline must READ, not extract.
type explodingExtractor struct{ t *testing.T }

func (e explodingExtractor) Extract(context.Context, string, []byte, func(entities.ExtractEvent)) (entities.Extraction, error) {
	e.t.Fatal("the outline extracted instead of reading the rendering")
	return entities.Extraction{}, nil
}

// fakeSteps records the step transitions a use case makes.
type fakeSteps struct {
	seen       map[entities.Step][]entities.StepStatus
	titles     map[string]string
	languages  map[string]string
	identities map[string]entities.SourceIdentity
	resets     []string
	parts      map[string]map[string]entities.PartRecord
	// index lets Release mirror the ownership removal, as the real tracker does.
	index *fakeIndex
}

func (f *fakeSteps) record(step entities.Step, st entities.StepStatus) {
	if f.seen == nil {
		f.seen = map[entities.Step][]entities.StepStatus{}
	}
	f.seen[step] = append(f.seen[step], st)
}

func (f *fakeSteps) Record(_ context.Context, _ string, step entities.Step, st entities.StepStatus) error {
	f.record(step, st)
	return nil
}
func (f *fakeSteps) Steps(string) (entities.StepSet, error) { return entities.NewStepSet(), nil }
func (f *fakeSteps) Records() (map[string]entities.SourceRecord, error) {
	return map[string]entities.SourceRecord{}, nil
}

func (f *fakeSteps) SetParts(hash string, parts map[string]entities.PartRecord) error {
	if f.parts == nil {
		f.parts = map[string]map[string]entities.PartRecord{}
	}
	f.parts[hash] = parts
	return nil
}

func (f *fakeSteps) SetTitle(hash, title string) error {
	if f.titles == nil {
		f.titles = map[string]string{}
	}
	f.titles[hash] = title
	return nil
}

func (f *fakeSteps) Reset(hash string) error {
	f.resets = append(f.resets, hash)
	return nil
}
func (f *fakeSteps) Remove(string) error { return nil }

// SetIdentity remembers what the source IS, keyed by hash.
func (f *fakeSteps) SetLanguage(hash, language string) error {
	if f.languages == nil {
		f.languages = map[string]string{}
	}
	f.languages[hash] = language
	return nil
}
func (f *fakeSteps) SetOrigin(string, string, string) error { return nil }

// Release records the manual-addition release by clearing the identity's ownership, and mirrors it
// into the index the way the real tracker does.
func (f *fakeSteps) Release(hash string) error {
	if id, ok := f.identities[hash]; ok {
		id.Origin, id.WikiPage, id.ParentHash = "", "", ""
		f.identities[hash] = id
	}
	if f.index != nil {
		_ = f.index.ClearSourceOrigin(context.Background(), hash)
	}
	return nil
}

func (f *fakeSteps) SetIdentity(hash string, id entities.SourceIdentity) error {
	if f.identities == nil {
		f.identities = map[string]entities.SourceIdentity{}
	}
	f.identities[hash] = id
	return nil
}

func (f *fakeSteps) Identity(hash string) (entities.SourceIdentity, error) {
	if f.identities == nil {
		return entities.SourceIdentity{}, nil
	}
	return f.identities[hash], nil
}

func (f *fakeSteps) Rehash(oldHash, newHash string) error {
	if f.identities == nil {
		return nil
	}
	if id, ok := f.identities[oldHash]; ok {
		delete(f.identities, oldHash)
		f.identities[newHash] = id
	}
	return nil
}

// TestCaptureWritesTheRenderingAndTheOutlineReadsIt is the capture/outline split as a test: step 1
// extracts and writes the readable rendering, step 2 produces the tree by PARSING it — never by
// extracting again. That is what makes a rebuild reproduce the derivation instead of invalidating
// it, and what makes the source readable in the vault without poppler or tesseract installed.
func TestCaptureWritesTheRenderingAndTheOutlineReadsIt(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}

	// A PDF: its bytes are not readable, which is exactly why step 1 exists.
	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "book.pdf", Reader: strings.NewReader("%PDF-1.7 not text")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if src.TreeState != string(entities.TreeNone) {
		t.Errorf("tree state = %q, want none", src.TreeState)
	}

	parts := []entities.Part{
		{Level: 1, Title: "1 Caching", Text: "Caching is only safe when the key covers every input.", Page: 12},
		{Level: 1, Title: "2 Memory", Text: "A memory model defines what a program may assume.", Page: 40},
	}
	steps := &fakeSteps{}
	extractor := &fakeExtractor{parts: parts}
	log := &fakeSourceLog{}
	capture := usecases.CaptureSource{
		Store: store, Index: idx, Extractor: extractor, Steps: steps,
		SourceLog: log, Clock: fixedClock{}, Log: nil,
	}
	res, err := capture.Execute(ctx, src.Hash)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	// Pages is the LAST page number of the document (what "512 pages" means for a book), not the
	// number of parts.
	if res.Parts != 2 || res.Pages != 40 || res.OCR {
		t.Errorf("capture = %+v, want 2 parts up to page 40", res)
	}
	if res.Rendered != src.Hash+entities.SidecarRender {
		t.Errorf("rendered = %q, want the rendering next to the object", res.Rendered)
	}
	rc, err := store.OpenRendered(src.Hash, entities.SidecarRender)
	if err != nil {
		t.Fatalf("the rendering is not in the vault: %v", err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !strings.Contains(string(raw), "# 1 Caching") || !strings.Contains(string(raw), "<!--part level=1 page=12-->") {
		t.Errorf("the rendering is not readable Markdown with structure:\n%s", raw)
	}

	// The step record says capture ran and succeeded — with the light the UI shows.
	states := steps.seen[entities.StepCapture]
	if len(states) != 2 || states[0].State != entities.StepRunning || states[1].State != entities.StepOK {
		t.Errorf("capture steps = %+v, want running then ok", states)
	}
	if !strings.Contains(states[1].Detail, "40 pages") {
		t.Errorf("detail = %q, want the page count", states[1].Detail)
	}

	// Step 2 reads: the extractor must NOT be called again.
	before := extractor.calls
	bt := usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: explodingExtractor{t}, Clock: fixedClock{}}
	tres, err := bt.Execute(ctx, src.Hash)
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	if extractor.calls != before {
		t.Error("the capture's extractor ran again during the outline")
	}
	want := tree.Build(parts)
	if len(trees.m[src.Hash]) != len(want) {
		t.Fatalf("tree nodes = %d, want %d", len(trees.m[src.Hash]), len(want))
	}
	for i := range want {
		if trees.m[src.Hash][i].Text != want[i].Text || trees.m[src.Hash][i].Title != want[i].Title {
			t.Errorf("node %d = %+v, want %+v", i, trees.m[src.Hash][i], want[i])
		}
	}
	if tres.Nodes != 2 {
		t.Errorf("nodes = %d, want 2", tres.Nodes)
	}
}

// TestCaptureFailureIsRecorded: a capture that cannot read the source must say so — the step is
// red, the outline waits, and nothing pretends the document is readable.
func TestCaptureFailureIsRecorded(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "scan.pdf", Reader: strings.NewReader("%PDF no text layer")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	steps := &fakeSteps{}
	capture := usecases.CaptureSource{
		Store: store, Index: idx, Extractor: &fakeExtractor{err: errors.New("no text layer and no OCR")},
		Steps: steps, Clock: fixedClock{},
	}
	if _, err := capture.Execute(ctx, src.Hash); err == nil {
		t.Fatal("a failed capture must return its error")
	}
	states := steps.seen[entities.StepCapture]
	if len(states) != 2 || states[1].State != entities.StepFailed {
		t.Fatalf("capture steps = %+v, want running then failed", states)
	}
	if !strings.Contains(states[1].Error, "no text layer") {
		t.Errorf("the failure reason must be recorded: %+v", states[1])
	}
}

// TestIntakeLeavesCaptureOpenForAPDF: the intake is the capture for Markdown, text and a URL
// snapshot (the object is the readable form) — and NOT for a PDF, where step 1 still has to read
// the bytes. An outline may not run before that, and the light shows it.
func TestIntakeLeavesCaptureOpenForAPDF(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	steps := &fakeSteps{}
	intake := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}, Steps: steps}

	if _, err := intake.Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# T\nbody\n")}); err != nil {
		t.Fatalf("intake md: %v", err)
	}
	md := steps.seen[entities.StepCapture][len(steps.seen[entities.StepCapture])-1]
	if md.State != entities.StepOK {
		t.Errorf("markdown capture = %q, want ok (the object is readable)", md.State)
	}

	if _, err := intake.Execute(ctx, usecases.IntakeInput{Name: "book.pdf", Reader: strings.NewReader("%PDF")}); err != nil {
		t.Fatalf("intake pdf: %v", err)
	}
	pdf := steps.seen[entities.StepCapture][len(steps.seen[entities.StepCapture])-1]
	if pdf.State != entities.StepPending {
		t.Errorf("pdf capture = %q, want pending (step 1 has to extract)", pdf.State)
	}

	// An EPUB is a container, not text: step 1 has to read its chapters, so the capture is open here
	// too — and the source is filed with the right kind.
	epubSrc, err := intake.Execute(ctx, usecases.IntakeInput{Name: "book.epub", Reader: strings.NewReader("PK\x03\x04")})
	if err != nil {
		t.Fatalf("intake epub: %v", err)
	}
	if epubSrc.Kind != string(entities.SourceKindEPUB) {
		t.Errorf("epub kind = %q, want epub", epubSrc.Kind)
	}
	book := steps.seen[entities.StepCapture][len(steps.seen[entities.StepCapture])-1]
	if book.State != entities.StepPending {
		t.Errorf("epub capture = %q, want pending (step 1 has to read the container)", book.State)
	}
}

// TestCaptureReadsABook: a book is captured like a PDF — its bytes are not text — and the title and
// language come from the book itself, not from the file name or a detector. Its spine documents are
// counted as chapters.
func TestCaptureReadsABook(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "book.epub", Reader: strings.NewReader("PK\x03\x04")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}

	steps := &fakeSteps{}
	capture := usecases.CaptureSource{
		Store: store, Index: idx, Steps: steps, Clock: fixedClock{},
		Extractor: &fakeExtractor{
			parts: []entities.Part{
				{Level: 1, Title: "Caching Basics", Text: "A cache is safe only when the key covers every input.", Page: 1},
				{Level: 1, Title: "Invalidation", Text: "Invalidation is the hard part.", Page: 2},
			},
			title:    "The Cache Book",
			language: "en",
		},
	}
	res, err := capture.Execute(ctx, src.Hash)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if res.Pages != 2 {
		t.Errorf("chapters = %d, want 2", res.Pages)
	}
	if got := steps.titles[src.Hash]; got != "The Cache Book" {
		t.Errorf("recorded title = %q, want the book's dc:title", got)
	}
	if got := steps.languages[src.Hash]; got != "en" {
		t.Errorf("recorded language = %q, want the declared one", got)
	}
	states := steps.seen[entities.StepCapture]
	if len(states) != 2 || !strings.Contains(states[1].Detail, "2 chapters") {
		t.Errorf("capture detail = %+v, want it to count chapters", states)
	}
}

var (
	_ = extract.New
	_ = gateways.ExecRunner{}
	_ = ulid.ULID{}
)
