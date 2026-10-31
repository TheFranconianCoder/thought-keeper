// Package extract turns raw source bytes into ordered parts. Markdown and text
// are pure Go; PDF uses poppler (pdftotext) with an ad-hoc OCR fallback
// (pdftocairo + tesseract) — subprocesses over stdin/stdout, never a daemon. An
// EPUB is a ZIP of XHTML and is read in pure Go, like the two text formats.
package extract

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Config configures the extractor, mirroring the pdf/ocr config blocks.
type Config struct {
	PDFTextTool         string
	Rasterizer          string
	Tesseract           string
	Languages           []string
	DPI                 int
	MinTextCharsPerPage int
	MaxPages            int
	OCR                 bool
	Log                 *slog.Logger
}

// Extractor dispatches by extension.
type Extractor struct {
	runner interfaces.ToolRunner
	cfg    Config
}

// New returns an extractor.
func New(runner interfaces.ToolRunner, cfg Config) *Extractor {
	return &Extractor{runner: runner, cfg: cfg}
}

// Extract returns the parts for the given extension.
func (e *Extractor) Extract(ctx context.Context, ext string, data []byte, onEvent func(entities.ExtractEvent)) (entities.Extraction, error) {
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		parts, err := markdownSections(data)
		return entities.Extraction{Parts: parts}, err
	case ".txt":
		parts, err := textSections(data)
		return entities.Extraction{Parts: parts}, err
	case ".pdf":
		parts, err := e.pdfSections(ctx, data, onEvent)
		return entities.Extraction{Parts: parts}, err
	case ".epub":
		return e.epubSections(data, onEvent)
	default:
		return entities.Extraction{}, types.New("unsupported_source",
			"only .md, .pdf, .txt and .epub sources are supported", types.ErrUnsupportedSource)
	}
}

// ocrPage rasterizes one page to stdout (pdftocairo, which supports `-` as the
// output prefix) and pipes it into tesseract via stdin.
func (e *Extractor) ocrPage(ctx context.Context, pdf []byte, page int) (string, error) {
	pdfArgs := []string{
		"-png", "-r", strconv.Itoa(e.cfg.DPI),
		"-f", strconv.Itoa(page), "-l", strconv.Itoa(page),
		"-singlefile", "-", "-",
	}
	img, err := e.runner.Run(ctx, e.cfg.Rasterizer, pdfArgs, pdf)
	if err != nil {
		return "", fmt.Errorf("rasterize page %d: %w", page, err)
	}
	if len(img) == 0 {
		return "", fmt.Errorf("rasterize page %d: no image produced (does %s write to stdout?)", page, e.cfg.Rasterizer)
	}
	langs := strings.Join(e.cfg.Languages, "+")
	text, err := e.runner.Run(ctx, e.cfg.Tesseract, []string{"stdin", "stdout", "-l", langs}, img)
	if err != nil {
		return "", fmt.Errorf("ocr page %d: %w", page, err)
	}
	return string(text), nil
}
