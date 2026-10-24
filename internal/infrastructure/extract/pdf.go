package extract

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// pdfSections extracts text with poppler's pdftotext (stdin → stdout). Pages are
// separated by a form feed. Pages whose text is shorter than the threshold are
// treated as scanned and OCR'd page by page (pdftocairo + tesseract) when available.
func (e *Extractor) pdfSections(ctx context.Context, data []byte, onEvent func(entities.ExtractEvent)) ([]entities.Part, error) {
	if !e.runner.Available(e.cfg.PDFTextTool) {
		return nil, types.New("unsupported_source",
			"pdftotext (poppler) is required for PDF text extraction", types.ErrUnsupportedSource)
	}

	out, err := e.runner.Run(ctx, e.cfg.PDFTextTool, []string{"-layout", "-", "-"}, data)
	if err != nil {
		return nil, fmt.Errorf("pdftotext: %w", err)
	}

	// A form feed separates pages. The split is over the BYTES poppler wrote: string(out) would
	// copy the entire extracted text again, and every page becomes a string only when it is kept.
	pages := [][]byte{}
	for page := range bytes.SplitSeq(out, []byte{12}) {
		pages = append(pages, page)
	}
	ocrReady := e.cfg.OCR && e.runner.Available(e.cfg.Rasterizer) && e.runner.Available(e.cfg.Tesseract)

	var pageTexts []pageText
	for i, page := range pages {
		if i == len(pages)-1 && strings.TrimSpace(string(page)) == "" {
			break // trailing form feed after the last page
		}
		pageNo := i + 1
		if e.cfg.MaxPages > 0 && pageNo > e.cfg.MaxPages {
			break
		}
		text := strings.TrimSpace(string(page))
		ocr := false
		if len(text) < e.cfg.MinTextCharsPerPage && ocrReady {
			ocrStart := time.Now()
			ocrText, err := e.ocrPage(ctx, data, pageNo)
			dur := time.Since(ocrStart)
			emit := func(ev entities.ExtractEvent) {
				if onEvent != nil {
					ev.Kind = "ocr"
					ev.Page = pageNo
					ev.Duration = dur
					onEvent(ev)
				}
			}
			switch {
			case err != nil:
				if e.cfg.Log != nil {
					e.cfg.Log.Warn("ocr failed", "page", pageNo, "err", err.Error())
				}
				emit(entities.ExtractEvent{Err: err})
			case strings.TrimSpace(ocrText) != "":
				text = strings.TrimSpace(ocrText)
				ocr = true
				if e.cfg.Log != nil {
					e.cfg.Log.Info("ocr page", "page", pageNo, "chars", len(text), "dur", dur.Round(time.Millisecond))
				}
				emit(entities.ExtractEvent{Chars: len(text)})
			default:
				if e.cfg.Log != nil {
					e.cfg.Log.Warn("ocr returned no text", "page", pageNo)
				}
				emit(entities.ExtractEvent{})
			}
		} else if len(text) < e.cfg.MinTextCharsPerPage && !ocrReady {
			if e.cfg.Log != nil {
				e.cfg.Log.Warn("page has no text layer and OCR is unavailable",
					"page", pageNo, "chars", len(text))
			}
		}
		pageTexts = append(pageTexts, pageText{no: pageNo, ocr: ocr, lines: strings.Split(text, "\n")})
	}

	// Page numbers and running heads are not the document's text: drop them before the parts are
	// shaped, so they can never become a claim.
	lines := make([][]string, len(pageTexts))
	for i, p := range pageTexts {
		lines[i] = p.lines
	}
	kept, dropped := stripPageFurniture(lines)
	if dropped > 0 {
		for i := range pageTexts {
			pageTexts[i].lines = kept[i]
		}
		if e.cfg.Log != nil {
			e.cfg.Log.Info("dropped page furniture", "lines", dropped, "pages", len(pageTexts))
		}
		if onEvent != nil {
			onEvent(entities.ExtractEvent{Kind: "furniture", Lines: dropped, Pages: len(pageTexts)})
		}
	}
	return partsFromPages(pageTexts), nil
}
