package entities_test

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestSourceKinds: a kind decides two things — whether step 1 has work to do (the bytes are not the
// readable form) and what the reader calls the count. A book is the case where both answers differ
// from a PDF: it must be captured, and its sequence is chapters, not pages.
func TestSourceKinds(t *testing.T) {
	cases := []struct {
		ext      string
		kind     entities.SourceKind
		capture  bool
		chapters bool
	}{
		{".md", entities.SourceKindMD, false, false},
		{".markdown", entities.SourceKindMD, false, false},
		{".txt", entities.SourceKindTXT, false, false},
		{".pdf", entities.SourceKindPDF, true, false},
		{".epub", entities.SourceKindEPUB, true, true},
		{".EPUB", entities.SourceKindEPUB, true, true},
	}
	for _, c := range cases {
		kind := entities.KindForExt(c.ext)
		if kind != c.kind {
			t.Errorf("KindForExt(%q) = %q, want %q", c.ext, kind, c.kind)
		}
		if got := kind.NeedsCapture(); got != c.capture {
			t.Errorf("%q NeedsCapture = %v, want %v", c.ext, got, c.capture)
		}
		if got := kind.HasChapters(); got != c.chapters {
			t.Errorf("%q HasChapters = %v, want %v", c.ext, got, c.chapters)
		}
	}

	if kind := entities.KindForExt(".docx"); kind != "" {
		t.Errorf("a .docx is not a source: got %q", kind)
	}
	if kind := entities.KindForExt(".zip"); kind != "" {
		t.Errorf("a .zip is not a source: got %q", kind)
	}
}
