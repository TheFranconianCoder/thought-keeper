package usecases

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// CaptureSource is step 1 for a source whose BYTES are not readable as they are: it extracts the
// text (poppler for a PDF, OCR for a scanned page) and writes the local, static rendering next to
// the object — sources/<ab>/<cd>/<hash>.md.
//
// Why a step of its own: what a document says must be readable again and again, and the outline must
// not have to extract anything. Parsing the rendering cannot disagree with the bytes, so a rebuild
// produces the same tree the derivation was built on instead of invalidating it. Markdown and text
// sources need no capture of their own: the object IS the readable form, and a URL snapshot is
// already Markdown.
type CaptureSource struct {
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Extractor interfaces.Extractor
	// Steps records the CAPTURE step (running/ok/failed) for the source's lights.
	Steps interfaces.StepTracker
	// Languages recognises the language of the extracted text. Nil means no detection.
	Languages interfaces.LanguageDetector
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	Log       *slog.Logger
}

// CaptureResult summarizes a capture.
type CaptureResult struct {
	Hash  string
	Parts int
	Chars int
	Pages int
	OCR   bool
	// Rendered is the path of the readable rendering (vault-relative), when one was written.
	Rendered string
}

// RenderExt is the suffix of a source's readable rendering: <hash>.capture.md next to <hash>.pdf —
// named after the step that writes it, a plain Markdown document (see entities.SidecarRender).
const RenderExt = entities.SidecarRender

// Execute captures one source: extract, write the rendering, record the step.
func (uc CaptureSource) Execute(ctx context.Context, hash string) (CaptureResult, error) {
	recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepCapture, entities.StepRunning, "", nil)
	res, err := uc.capture(ctx, hash)
	if err != nil {
		recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepCapture, entities.StepFailed, "", err)
		return res, err
	}
	detail := string(uc.kindOf(ctx, hash))
	if res.Pages > 0 {
		detail += " · " + itoa(res.Pages) + " pages"
	}
	detail += " · " + itoa(res.Chars) + " chars"
	if res.Rendered != "" {
		detail += " · readable"
	}
	if res.OCR {
		detail += " · OCR"
	}
	recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepCapture, entities.StepOK, detail, nil)
	return res, nil
}

func (uc CaptureSource) capture(ctx context.Context, hash string) (CaptureResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return CaptureResult{}, err
	}
	rc, err := uc.Store.Open(hash, src.Ext)
	if err != nil {
		return CaptureResult{}, err
	}
	defer func() { _ = rc.Close() }()

	// Every OCR page is slow and lossy: report it where the source lives. Page furniture (page
	// numbers, running heads) the extractor dropped is reported there too — text leaving a source
	// is not silent.
	onExtract := func(ev entities.ExtractEvent) {
		if ev.Kind == "furniture" {
			uc.logSource(ctx, hash, "info", "furniture", "dropped page furniture", map[string]any{
				"lines": ev.Lines, "pages": ev.Pages,
			})
			return
		}
		level, msg := "info", "ocr page"
		fields := map[string]any{
			"page": ev.Page, "chars": ev.Chars,
			"dur": ev.Duration.Round(time.Millisecond).String(),
		}
		switch {
		case ev.Err != nil:
			level, msg, fields["err"] = "warn", "ocr failed", ev.Err
		case ev.Chars == 0:
			level, msg = "warn", "ocr returned no text"
		}
		uc.logSource(ctx, hash, level, "ocr", msg, fields)
	}

	start := uc.Clock.Now()
	// The extractor takes the object whole: every format needs all of it (a PDF is handed to
	// poppler on stdin, text is split into parts), and streaming only hides a second buffering.
	data, err := io.ReadAll(rc)
	if err != nil {
		return CaptureResult{}, err
	}
	parts, err := uc.Extractor.Extract(ctx, src.Ext, data, onExtract)
	if err != nil {
		return CaptureResult{}, err
	}
	chars := 0
	for _, sec := range parts {
		chars += len(sec.Text)
	}
	pages, ocr := summarize(parts)
	if uc.Languages != nil {
		var sample strings.Builder
		for _, part := range parts {
			if sample.Len() >= 128<<10 {
				break
			}
			sample.WriteString(part.Text)
			sample.WriteByte(' ')
		}
		if language := uc.Languages.Detect(sample.String()); language != "" {
			if uc.Steps != nil {
				_ = uc.Steps.SetLanguage(hash, language)
			} else if uc.Index != nil {
				_ = uc.Index.SetSourceLanguage(ctx, hash, language)
			}
			uc.logSource(ctx, hash, "info", "language", "source language detected", map[string]any{"language": language})
		}
	}

	// STEP 1 DETERMINES THE TITLE, and for a PDF this is the first moment the content is readable:
	// the file name is replaced by the document's own heading (the outline may refine it with the
	// model later — it has the whole outline to look at).
	title, titleSource := DocumentTitle(src, parts, "")
	if uc.Steps != nil {
		if err := uc.Steps.SetTitle(hash, title); err != nil && uc.Log != nil {
			uc.Log.Warn("capture: could not record the title", "hash", hash, "err", err)
		}
	}
	uc.logSource(ctx, hash, "info", "title", "document title determined", map[string]any{
		"title": title, "source": titleSource, "original": src.OriginalName, "url": src.URL,
	})

	out := CaptureResult{Hash: hash, Parts: len(parts), Chars: chars, Pages: pages, OCR: ocr}
	// The rendering: the document as Markdown, in the vault, openable without poppler. It carries
	// the structure the outline reads (level, page, OCR marker per part).
	if err := uc.Store.PutRendered(hash, RenderExt, []byte(tree.Render(parts))); err != nil {
		return out, err
	}
	// A vault written before the rendering was renamed still carries the same document under an old
	// name (<hash>.text.md, or a plain <hash>.md): it goes. The store never removes the source's own
	// object, so a Markdown source is safe even though the legacy list contains ".md".
	for _, legacy := range entities.SidecarRenderRenames {
		// A plain ".md" is the OBJECT of a Markdown source (a wiki page included), not only a
		// legacy rendering. Removing it here deleted the bytes the capture had just read.
		if strings.EqualFold(legacy, src.Ext) {
			continue
		}
		if err := uc.Store.RemoveRendered(hash, legacy); err != nil && uc.Log != nil {
			uc.Log.Warn("capture: could not drop the old rendering", "hash", hash, "name", legacy, "err", err)
		}
	}
	out.Rendered = hash + RenderExt
	uc.logSource(ctx, hash, "info", "capture", "text extracted and rendered", map[string]any{
		"kind": src.Kind, "pages": pages, "chars": chars, "ocr": ocr,
		"file": out.Rendered,
		"dur":  uc.Clock.Now().Sub(start).Round(time.Millisecond).String(),
	})
	return out, nil
}

func (uc CaptureSource) kindOf(ctx context.Context, hash string) entities.SourceKind {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return ""
	}
	return src.Kind
}

func (uc CaptureSource) logSource(ctx context.Context, hash, level, phase, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
