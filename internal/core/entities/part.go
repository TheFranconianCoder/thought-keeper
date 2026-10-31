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
	// Page is the 1-based position in the format's own sequence: a PDF's page, an EPUB's spine
	// document (its chapter). 0 when the format has neither.
	Page int
	// OCR marks text that came from OCR, so a lossy extraction stays visible.
	OCR bool
}

// Extraction is one extraction's result: the parts, plus what the FORMAT itself declares about the
// document. The declared values are what the format says, not what a reader guesses: an EPUB carries
// its own dc:title and dc:language, and those beat the file name and a language detector. A format
// that declares nothing leaves both empty.
type Extraction struct {
	Parts []Part
	// Title is the document's own declared title (an EPUB's dc:title), never a heading.
	Title string
	// Language is the declared language as the vault stores it — ISO 639-1, or ISO 639-3 when no
	// 639-1 exists; "" when the format does not say.
	Language string
}

// ExtractEvent is progress from extraction. OCR is slow and lossy, so every page it touches is
// reported: which page, how much text came out, how long it took, or why it failed. The
// page-furniture filter reports what it removed (Kind "furniture"), for the same reason: taking
// text away from a source is something its log has to say. Kind "images" reports what a container
// format dropped because this vault reads text (an EPUB's figures and cover), and Kind "skipped"
// reports a chapter a container could not be read from.
type ExtractEvent struct {
	Kind string // "ocr" | "furniture" | "images" | "skipped"
	Page int
	// Lines and Pages say how much page furniture (page numbers, running heads) was dropped —
	// Kind "furniture" only.
	Lines int
	Pages int
	Chars int
	// Count is how many things a non-text event concerns (images dropped) — Kind "images" only.
	Count    int
	Duration time.Duration
	Err      error
}
