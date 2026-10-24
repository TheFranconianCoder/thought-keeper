package entities

import "time"

// Part is one piece of an extracted document, in document order. Extraction is
// deterministic: the same bytes always produce the same parts, which is what lets the
// tree be rebuilt from them.
type Part struct {
	// Level is the heading depth (1 = top level). 0 marks text before the first heading.
	Level int
	Title string
	Text  string
	// TextStart/TextEnd are the part's text as a RANGE in the readable text it was parsed from
	// (the capture rendering, or the object itself when its bytes are readable). The outline stores
	// this reference instead of the text: the text is already there, and a tree that copies it
	// duplicates the whole document.
	TextStart int
	TextEnd   int
	// Page is the 1-based page number for PDFs (0 when the format has no pages).
	Page int
	// OCR marks text that came from OCR, so a lossy extraction stays visible.
	OCR bool
}

// ExtractEvent is progress from extraction. OCR is slow and lossy, so every page it touches is
// reported: which page, how much text came out, how long it took, or why it failed. The
// page-furniture filter reports what it removed (Kind "furniture"), for the same reason: taking
// text away from a source is something its log has to say.
type ExtractEvent struct {
	Kind string // "ocr" | "furniture"
	Page int
	// Lines and Pages say how much page furniture (page numbers, running heads) was dropped —
	// Kind "furniture" only.
	Lines    int
	Pages    int
	Chars    int
	Duration time.Duration
	Err      error
}
