package usecases

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// IntakeSource stores raw bytes into the content-addressed store and records the
// source in the index.
type IntakeSource struct {
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	// Steps records the CAPTURE step: the source is taken in and readable locally from now on.
	Steps interfaces.StepTracker
	// Languages recognises the source's language from its own text. Nil means no detection.
	Languages interfaces.LanguageDetector
}

// titleFromObject reads a source's own text and derives its title from it (step 1 for a document
// whose content is readable as it is). It is best-effort: a file that cannot be read keeps the name
// it was dropped with.
func (uc IntakeSource) titleFromObject(ctx context.Context, hash, ext string, kind entities.SourceKind, fallback string) (string, string) {
	rc, err := uc.Store.Open(hash, ext)
	if err != nil {
		return "", ""
	}
	defer func() { _ = rc.Close() }()
	// Only the beginning can hold a title, and a source whose content is readable as it is is text:
	// a bounded read is enough and keeps a large .txt from being loaded twice.
	const head = 64 << 10
	buf := make([]byte, head)
	n, _ := io.ReadFull(rc, buf)
	if n <= 0 {
		return "", ""
	}
	src := entities.Source{Hash: hash, Kind: kind, Title: fallback}
	return DocumentTitle(src, nil, string(buf[:n]))
}

// languageFromObject detects the language of a source whose bytes are readable as they are. It is
// best-effort: a detector that cannot classify short or code-heavy text leaves the field empty.
func (uc IntakeSource) languageFromObject(ctx context.Context, hash, ext string) string {
	if uc.Languages == nil {
		return ""
	}
	rc, err := uc.Store.Open(hash, ext)
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	const sample = 128 << 10
	buf := make([]byte, sample)
	n, _ := io.ReadFull(rc, buf)
	if n <= 0 {
		return ""
	}
	return uc.Languages.Detect(string(buf[:n]))
}

// IntakeInput is the input for a single intake.
type IntakeInput struct {
	Name   string // original filename; its extension selects the extractor
	Reader io.Reader
}

// Execute hashes and stores the content, then upserts the index row.
func (uc IntakeSource) Execute(ctx context.Context, in IntakeInput) (partialentities.PartialSource, error) {
	ext := strings.ToLower(filepath.Ext(in.Name))
	kind := entities.KindForExt(ext)
	if kind == "" {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source",
			"only .md, .pdf and .txt sources are supported",
			types.ErrUnsupportedSource,
		)
	}
	obj, err := uc.Store.Put(ctx, ext, in.Reader)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	// STEP 1 DETERMINES THE TITLE. The bytes are in the store now, so for a source whose content is
	// readable as it is (Markdown, text) the title is derived from the document itself — not left as
	// the file name until some later step happens to run. A PDF has no readable text yet: it keeps
	// the file name here and the capture step refines it.
	title := strings.TrimSuffix(filepath.Base(in.Name), ext)
	titleSource := "filename"
	if !kind.NeedsCapture() {
		if t, from := uc.titleFromObject(ctx, obj.Hash, obj.Ext, kind, title); t != "" {
			title, titleSource = t, from
		}
	}

	src := entities.Source{
		ID:    ulid.Make(),
		Hash:  obj.Hash,
		Kind:  kind,
		Ext:   obj.Ext,
		Path:  obj.Path,
		Title: title,
		// The raw input keeps its own name: the title comes from the document's content (the
		// model names it), the file name is shown additionally.
		OriginalName: filepath.Base(in.Name),
		Bytes:        obj.Bytes,
		TreeState:    entities.TreeNone,
		ImportedAt:   uc.Clock.Now(),
		Meta:         "{}",
	}
	if !kind.NeedsCapture() {
		src.Language = uc.languageFromObject(ctx, obj.Hash, obj.Ext)
	}
	if err := uc.Index.UpsertSource(ctx, src); err != nil {
		return partialentities.PartialSource{}, err
	}
	if src.Language != "" {
		_ = uc.Index.SetSourceLanguage(ctx, obj.Hash, src.Language)
		if uc.Steps != nil {
			_ = uc.Steps.SetLanguage(obj.Hash, src.Language)
		}
	}
	// Read back: on a deduplicated intake the stored row keeps its original id.
	stored, err := uc.Index.GetSourceByHash(ctx, obj.Hash)
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, src.Hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "intake", Level: "info", Message: "source stored",
			Fields: map[string]any{
				"kind": kind, "ext": obj.Ext, "bytes": obj.Bytes,
				"original": filepath.Base(in.Name), "deduped": err != nil,
			},
		})
	}
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, src.Hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "title", Level: "info", Message: "document title determined",
			Fields: map[string]any{"title": title, "source": titleSource, "original": filepath.Base(in.Name)},
		})
	}
	if uc.Steps != nil {
		// Best-effort: the record is bookkeeping next to a source that IS stored, so a failure here
		// must not fail the intake.
		_ = uc.Steps.SetTitle(src.Hash, title)
		// The object's file name carries the extension, not what the source IS: record the kind and
		// the name it came in under, so a reindex does not have to guess (see entities.SourceIdentity).
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{
			Kind: kind, OriginalName: filepath.Base(in.Name), Language: src.Language,
		})
	}

	// Capture is the INTAKE for a source whose bytes are already the readable form (Markdown, text,
	// a URL snapshot): it is stored, local, immutable and readable again and again — done.
	if !kind.NeedsCapture() {
		recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK,
			fmt.Sprintf("%s · %d bytes", kind, obj.Bytes), nil)
	} else {
		// A PDF's bytes are not readable: step 1 has to extract the text (and OCR what has no text
		// layer). Until that has happened the capture is OPEN — and the outline waits for it,
		// because an outline of bytes nobody can read is not an outline.
		recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepPending,
			fmt.Sprintf("%s · %d bytes · text extraction pending", kind, obj.Bytes), nil)
	}
	if err != nil {
		return partialentities.PartialFromSource(src), nil
	}
	return partialentities.PartialFromSource(stored), nil
}
