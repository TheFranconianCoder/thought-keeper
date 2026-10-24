package usecases_test

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// TestDocumentTitleIsDeterminedInStepOne: the title belongs to the document, and it has to be there
// as soon as the source is readable. A title that only appears once the outline ran is a title that
// is missing whenever the outline did not run — the way a vault ends up with a list of hash names.
func TestDocumentTitleIsDeterminedInStepOne(t *testing.T) {
	cases := []struct {
		name  string
		src   entities.Source
		parts []entities.Part
		text  string
		want  string
		from  string
	}{
		{
			name: "a URL keeps its declared title",
			src:  entities.Source{Kind: entities.SourceKindURL, Title: "The Original Page Title", Hash: "aa"},
			text: "# Something Else",
			want: "The Original Page Title", from: "declared",
		},
		{
			// The first heading of an EXTRACTED document is whatever the first page happened to start
			// with — a chapter number, "Contents", a running head. The file name is what the reader
			// called it, and the outline step asks the model to name it properly; taking the first part
			// would put "Page 1" or "Contents" in the source list.
			name:  "an extracted document keeps its file name",
			src:   entities.Source{Kind: entities.SourceKindPDF, Title: "scan-2024", Hash: "bb"},
			parts: []entities.Part{{Level: 1, Title: "Caching and Correctness", Text: "…"}},
			want:  "scan-2024", from: "filename",
		},
		{
			name:  "a page marker is no title either",
			src:   entities.Source{Kind: entities.SourceKindPDF, Title: "scan-2024", Hash: "cc"},
			parts: []entities.Part{{Level: 1, Title: "Page 1", Text: "…"}},
			want:  "scan-2024", from: "filename",
		},
		{
			// Without a file name (an older vault), the heading is still better than the hash.
			name:  "an extracted document with no file name falls back to its heading",
			src:   entities.Source{Kind: entities.SourceKindPDF, Hash: "cc"},
			parts: []entities.Part{{Level: 1, Title: "Caching and Correctness", Text: "…"}},
			want:  "Caching and Correctness", from: "heading",
		},
		{
			name: "a Markdown heading names the document",
			src:  entities.Source{Kind: entities.SourceKindMD, Title: "notes", Hash: "dd"},
			text: "# Caching\n\nprose",
			want: "Caching", from: "heading",
		},
		{
			name: "Markdown without a heading keeps its file name",
			src:  entities.Source{Kind: entities.SourceKindMD, Title: "notes", Hash: "ee"},
			text: "just prose, no heading at all\n",
			want: "notes", from: "filename",
		},
		{
			name: "a plain text file may use its first line",
			src:  entities.Source{Kind: entities.SourceKindTXT, Title: "chapter-3", Hash: "ff"},
			text: "Chapter 3: Caching\nand more",
			want: "Chapter 3: Caching", from: "heading",
		},
		{
			name: "a text file that starts with a paragraph keeps its file name",
			src:  entities.Source{Kind: entities.SourceKindTXT, Title: "notes", Hash: "11"},
			text: "This is a long sentence that goes on and on without being a title at all, really.",
			want: "notes", from: "filename",
		},
		{
			name: "nothing to go on: the hash",
			src:  entities.Source{Kind: entities.SourceKindTXT, Hash: "1234567890ab"},
			want: "1234567890ab", from: "hash",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, from := usecases.DocumentTitle(c.src, c.parts, c.text)
			if got != c.want || from != c.from {
				t.Errorf("title = %q (%s), want %q (%s)", got, from, c.want, c.from)
			}
		})
	}
}
