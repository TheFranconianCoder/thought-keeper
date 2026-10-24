package extract_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

func TestMarkdownHeadingsAndFrontmatter(t *testing.T) {
	md := "---\ntitle: x\n---\n# Alpha\nintro\n## Beta\nbody\n```\n# not a heading\n```\n"
	secs, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".md", []byte(md), nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(secs) != 2 {
		t.Fatalf("parts = %d, want 2: %+v", len(secs), secs)
	}
	if secs[0].Title != "Alpha" || secs[0].Level != 1 {
		t.Errorf("sec0 = %+v", secs[0])
	}
	if secs[1].Title != "Beta" || secs[1].Level != 2 {
		t.Errorf("sec1 = %+v", secs[1])
	}
	if !strings.Contains(secs[1].Text, "# not a heading") {
		t.Errorf("fenced heading leaked: %q", secs[1].Text)
	}
}

func TestTextParagraphs(t *testing.T) {
	secs, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".txt", []byte("first line\nbody\n\nsecond\n"), nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(secs) != 2 {
		t.Fatalf("parts = %d, want 2", len(secs))
	}
	if secs[0].Title != "first line" {
		t.Errorf("title = %q", secs[0].Title)
	}
}

func TestUnsupportedExtension(t *testing.T) {
	_, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".docx", []byte("x"), nil)
	if !errors.Is(err, types.ErrUnsupportedSource) {
		t.Fatalf("err = %v, want ErrUnsupportedSource", err)
	}
}

// fakeRunner lets us test the PDF path without poppler installed.
type fakeRunner struct {
	avail map[string]bool
	out   map[string][]byte
}

func (f fakeRunner) Available(name string) bool { return f.avail[name] }
func (f fakeRunner) Run(_ context.Context, name string, _ []string, _ []byte) ([]byte, error) {
	return f.out[name], nil
}

func TestPDFPagesAndOCRFallback(t *testing.T) {
	runner := fakeRunner{
		avail: map[string]bool{"pdftotext": true, "pdftocairo": true, "tesseract": true},
		out: map[string][]byte{
			// page 1 has a real text layer; page 2 is "scanned" (empty)
			"pdftotext":  []byte("Real text on page one\f\f"),
			"pdftocairo": []byte("PNG"),
			"tesseract":  []byte("OCR recovered page two"),
		},
	}
	e := extract.New(runner, extract.Config{
		PDFTextTool: "pdftotext", Rasterizer: "pdftocairo", Tesseract: "tesseract",
		Languages: []string{"eng"}, DPI: 300, MinTextCharsPerPage: 10, MaxPages: 100, OCR: true,
	})
	secs, err := e.Extract(context.Background(), ".pdf", []byte("%PDF"), nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(secs) != 2 {
		t.Fatalf("parts = %d, want 2: %+v", len(secs), secs)
	}
	if !strings.Contains(secs[0].Text, "Real text") || secs[0].OCR {
		t.Errorf("page1 = %+v", secs[0])
	}
	if !secs[1].OCR || !strings.Contains(secs[1].Text, "OCR recovered") {
		t.Errorf("page2 = %+v", secs[1])
	}
}

func TestPDFRequiresPdftotext(t *testing.T) {
	e := extract.New(fakeRunner{avail: map[string]bool{}}, extract.Config{PDFTextTool: "pdftotext"})
	_, err := e.Extract(context.Background(), ".pdf", []byte("%PDF"), nil)
	if !errors.Is(err, types.ErrUnsupportedSource) {
		t.Fatalf("err = %v, want ErrUnsupportedSource", err)
	}
}

// TestPDFRealTool runs the actual poppler extraction on a generated one-page PDF.
// TestOCFEventsReportEveryPage: OCR is slow and lossy, so every page it touches is reported
// (the pipeline writes these into the source's own log).
func TestOCREventsReportEveryPage(t *testing.T) {
	runner := fakeRunner{avail: map[string]bool{
		"pdftotext": true, "pdftocairo": true, "tesseract": true,
	}, out: map[string][]byte{
		"pdftotext":  []byte("Real text page one\n\fscanned\n\f"),
		"pdftocairo": []byte("png"),
		"tesseract":  []byte("OCR recovered page two"),
	}}
	e := extract.New(runner, extract.Config{
		PDFTextTool: "pdftotext", Rasterizer: "pdftocairo", Tesseract: "tesseract",
		Languages: []string{"eng"}, DPI: 300, MinTextCharsPerPage: 10, MaxPages: 100, OCR: true,
	})
	var events []entities.ExtractEvent
	if _, err := e.Extract(context.Background(), ".pdf", []byte("%PDF"), func(ev entities.ExtractEvent) {
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (only the scanned page): %+v", len(events), events)
	}
	if events[0].Kind != "ocr" || events[0].Page != 2 || events[0].Chars == 0 || events[0].Err != nil {
		t.Errorf("event = %+v, want ocr page 2 with text", events[0])
	}
}

// TestPDFDropsPageFurnitureAndFindsHeadings: the whole path. A book page carries a running head, a
// page number and numbered headings. None of that furniture may survive into a part — it would
// become a claim — and the headings must shape the parts instead of "Page N".
func TestPDFDropsPageFurnitureAndFindsHeadings(t *testing.T) {
	pages := []string{
		"Concurrency in Practice      41\n1 Introduction\nThe cache is safe only when the key covers every input.\n41\n",
		"Concurrency in Practice      42\n1.1 Motivation\nProse on page two.\n42\n",
		"Concurrency in Practice      43\n2 Results\nNumbers on page three.\n43\n",
	}
	runner := fakeRunner{
		avail: map[string]bool{"pdftotext": true},
		out:   map[string][]byte{"pdftotext": []byte(strings.Join(pages, "\f") + "\f")},
	}
	e := extract.New(runner, extract.Config{PDFTextTool: "pdftotext", MinTextCharsPerPage: 10, MaxPages: 100})

	var events []entities.ExtractEvent
	secs, err := e.Extract(context.Background(), ".pdf", []byte("%PDF"),
		func(ev entities.ExtractEvent) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, s := range secs {
		if strings.Contains(s.Text, "Concurrency in Practice") {
			t.Errorf("the running head reached a part: %+v", s)
		}
		for line := range strings.SplitSeq(s.Text, "\n") {
			if strings.TrimSpace(line) == "41" || strings.TrimSpace(line) == "42" || strings.TrimSpace(line) == "43" {
				t.Errorf("a page number reached a part: %+v", s)
			}
		}
	}
	titles := make([]string, 0, len(secs))
	for _, s := range secs {
		titles = append(titles, s.Title)
	}
	want := []string{"1 Introduction", "1.1 Motivation", "2 Results"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Errorf("titles = %v, want the document's headings", titles)
	}
	if secs[1].Level != 2 || secs[1].Page != 2 {
		t.Errorf("the sub-part = %+v, want level 2 on page 2", secs[1])
	}

	// Dropping text is reported: the source's own log has to say it.
	dropped := 0
	for _, ev := range events {
		if ev.Kind == "furniture" {
			dropped = ev.Lines
		}
	}
	if dropped != 6 {
		t.Errorf("the furniture event reports %d lines, want 6 (a head and a number per page): %+v", dropped, events)
	}
}

// TestPDFRealTool runs the actual poppler extraction on a generated one-page PDF.
func TestPDFRealTool(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	runner := gateways.ExecRunner{}
	if !runner.Available("pdftotext") {
		t.Skip("pdftotext not installed")
	}
	e := extract.New(runner, extract.Config{PDFTextTool: "pdftotext", MinTextCharsPerPage: 32, MaxPages: 100})
	secs, err := e.Extract(context.Background(), ".pdf", minimalPDF("Hello Phase Three"), nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(secs) != 1 {
		t.Fatalf("parts = %d, want 1", len(secs))
	}
	if !strings.Contains(secs[0].Text, "Hello Phase Three") {
		t.Errorf("text = %q, want the page text", secs[0].Text)
	}
}

// minimalPDF builds a valid one-page PDF with a text layer and a correct xref.
func minimalPDF(text string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	stream := "BT /F1 24 Tf 72 720 Td (" + text + ") Tj ET"
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xref)
	return buf.Bytes()
}
