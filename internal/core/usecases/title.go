package usecases

import (
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
)

// DocumentTitle determines a source's title, and where it came from.
//
// Step 1 does this, not step 2: the title belongs to the document, and it has to be there as soon as
// the source is readable. A title that only appears after the outline was built is a title that is
// missing whenever the outline did not run — which is exactly how a vault ends up with a list of
// hash-named sources. The outline may REFINE it later with the model (it has the whole outline to
// look at); this chain is the deterministic floor underneath:
//
//	declared (a URL's og:title/<title>) → [a PDF's file name] → the first real heading → the file name → the hash
//
// Either parts (an extracted document) or text (raw Markdown/text) may be empty — a capture has
// parts, an intake has text.
func DocumentTitle(src entities.Source, parts []entities.Part, text string) (string, string) {
	// A wiki page's title is authored by the human, not inferred from a heading: it is the page's
	// name, and an edit must not rename the source behind its back.
	if src.IsWiki() {
		if t := strings.TrimSpace(src.Title); t != "" {
			return t, "wiki"
		}
	}
	if src.Kind == entities.SourceKindURL {
		if t := strings.TrimSpace(src.Title); t != "" {
			return t, "declared"
		}
	}
	// A PDF's first extracted heading is NOT a title: it is whatever the first page happened to start
	// with — a chapter number, "Contents", a running head. The file name is what the reader called it,
	// so it comes first, and the outline step asks the model to name the document properly (it sees the
	// whole outline, which is the only view from which a title means something). The deterministic floor
	// underneath stays the file name, never the first part.
	if src.Kind == entities.SourceKindPDF {
		if t := strings.TrimSpace(src.Title); t != "" {
			return t, "filename"
		}
	}
	if heading := pickTitle("", parts); heading != "" {
		return heading, "heading"
	}
	// A plain text file has no headings, so its first line can be its title — but only there:
	// in Markdown a title IS a heading, and a first line of prose is content.
	if heading := firstHeadingIn(text, src.Kind == entities.SourceKindTXT); heading != "" {
		return heading, "heading"
	}
	if t := strings.TrimSpace(src.Title); t != "" {
		return t, "filename"
	}
	if len(src.Hash) >= 12 {
		return src.Hash[:12], "hash"
	}
	return "untitled", "none"
}

// firstHeadingIn reads a title out of raw Markdown or text: the first ATX heading, or — for plain
// text, which has no headings — the first non-empty line when it looks like a title (short, no
// sentence punctuation) rather than prose.
func firstHeadingIn(text string, allowPlainLine bool) string {
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			if h := strings.TrimSpace(strings.TrimLeft(t, "#")); h != "" && len(h) <= 200 {
				return h
			}
			// A bare "#" is a heading with nothing in it: keep looking.
			continue
		}
		// Only the FIRST non-empty line can be a title, and only if it reads like one. A paragraph
		// (long, or ending in a full stop) is content, not a title — then the file name is better.
		if allowPlainLine && len(t) <= 120 && !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, ":") {
			return t
		}
		return ""
	}
	return ""
}

// isPageTitle reports whether a heading is just a page marker ("Page 12"), which is no title.
func isPageTitle(t string) bool {
	return strings.HasPrefix(t, "Page ") && len(t) <= len("Page 999999")
}

// pickTitle prefers a real heading; page titles ("Page N") and site chrome are ignored, so a PDF
// keeps its file name as the title instead of being called "Page 1".
func pickTitle(fallback string, parts []entities.Part) string {
	for _, s := range parts {
		t := strings.TrimSpace(s.Title)
		if t == "" || isPageTitle(t) || tree.IsBoilerplate(s) {
			continue
		}
		return t
	}
	return fallback
}
