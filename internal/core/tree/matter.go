package tree

import (
	"regexp"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// Front and back matter belongs to a book as an ARTEFACT, not to its subject: acknowledgements, a
// dedication, the copyright page, the table of contents, the index, the bibliography. They are pages
// of a book, not knowledge about what the book is about — atomizing them produces claims like "the
// author thanks their family" or "Knuth 1997 is listed", which nobody asked for and which then have
// to be curated out by hand.
//
// The match is EXACT after normalisation (lowercase, ASCII spelling, no punctuation, no
// "(continued)", no leading numbering), on the TITLE only. That is what keeps content content: a
// chapter called "Indexing strategies", "Dedication to quality" or "Notes on memory models" is not
// "Index", "Dedication" or "Notes", and a part whose *body* merely mentions an index is not
// affected at all.
//
// Unlike the chrome PHRASES in tree.go this is not limited to short parts: an acknowledgements
// part is acknowledgements at any length.
var matterTitles = map[string]bool{
	// Front matter
	"title page": true, "half title": true, "frontispiece": true, "copyright": true,
	"copyright page": true, "colophon": true, "imprint": true, "table of contents": true,
	"contents": true, "list of figures": true, "list of tables": true,
	"list of illustrations": true, "list of abbreviations": true, "list of acronyms": true,
	"dedication": true, "epigraph": true, "praise": true, "praise for this book": true,
	"also by": true, "also by the author": true, "other books by this author": true,
	"about the author": true, "about the authors": true, "about the publisher": true,
	// Back matter
	// An appendix and a glossary are NOT here on purpose: proofs, tables and definitions are
	// knowledge about the subject, not pages about the book.
	"acknowledgements": true, "acknowledgments": true,
	"notes": true, "endnotes": true, "footnotes": true, "bibliography": true, "references": true,
	"works cited": true, "further reading": true, "index": true, "permissions": true,
	"credits": true, "photo credits": true, "illustration credits": true,
	// German publishing conventions (the house language)
	"impressum": true, "inhalt": true, "inhaltsverzeichnis": true, "widmung": true,
	"danksagung": true, "danksagungen": true, "ueber den autor": true, "ueber die autoren": true,
	"ueber den verlag": true, "register": true, "literaturverzeichnis": true, "literatur": true,
	"anmerkungen": true, "abkuerzungen": true, "abbildungsverzeichnis": true,
	"tabellenverzeichnis": true, "bildnachweis": true, "weiterfuehrende literatur": true,
	"quellenverzeichnis": true, "autorinnen und autoren": true,
}

var (
	// "Acknowledgements (continued)", "Danksagung (Fortsetzung)", "Index, cont."
	matterSuffix = regexp.MustCompile(`\s*[\(\[]?\s*(cont|contd|continued|fortsetzung|fortgesetzt)\s*[\)\]]?\.?$`)
	// "Chapter 12 Notes", "Kapitel 3 Danksagung", "Anhang B"
	matterPrefix = regexp.MustCompile(`^(chapter|kapitel|part|teil|anhang|appendix)\s+[0-9ivxlcdm]{1,4}[.):]?\s+`)
	// "17 Acknowledgements", "II Danksagung", "3.1 Notes"
	matterNumber = regexp.MustCompile(`^[0-9ivxlcdm]{1,4}(\.[0-9]{1,3})*[.):]?\s+`)
	matterTrim   = regexp.MustCompile(`^[\s\p{P}]+|[\s\p{P}]+$`)
	matterFolds  = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")
)

var (
	pdfPageTitle = regexp.MustCompile(`(?i)^page\s+[0-9]+$`)
	tocLine      = regexp.MustCompile(`(?m)^\s*\S.*(?:\.\s*){3,}\s*[0-9]+\s*$`)
)

// IsPageMatter reports a PDF page that is front matter rather than content: a cover, a title page
// or a table of contents. PDF extraction gives those pages the generic title "Page N", so the
// title-only matter match above cannot see them. The tests are deliberately narrow: a page title
// must be the extractor's "Page N", a TOC must show repeated dot leaders with page numbers, and a
// cover/title page must be early, short and contain no sentence punctuation.
func IsPageMatter(s entities.Part) bool {
	if s.Page <= 0 || !pdfPageTitle.MatchString(strings.TrimSpace(s.Title)) {
		return false
	}
	text := strings.TrimSpace(s.Text)
	if text == "" {
		return true
	}
	if len(tocLine.FindAllString(text, -1)) >= 2 {
		return true
	}
	return s.Page <= 5 && len(text) <= 300 && !strings.ContainsAny(text, ".!?")
}

// IsMatter reports whether a part is front or back matter rather than the subject: the pages a
// published book carries around its content.
func IsMatter(s entities.Part) bool {
	key := matterKey(s.Title)
	return key != "" && matterTitles[key]
}

// matterKey normalises a title for the exact comparison: lowercase, ASCII spelling, no surrounding
// punctuation, no trailing "(continued)", no leading numbering — so "17. Danksagung (Fortsetzung)" is
// the acknowledgements of a German edition, and "Indexing strategies" is a chapter.
func matterKey(title string) string {
	t := matterFolds.Replace(strings.ToLower(strings.TrimSpace(title)))
	if t == "" {
		return ""
	}
	t = matterPrefix.ReplaceAllString(t, "")
	t = matterNumber.ReplaceAllString(t, "")
	t = matterSuffix.ReplaceAllString(t, "")
	t = matterTrim.ReplaceAllString(t, "")
	return strings.Join(strings.Fields(t), " ")
}

// DroppedTitles names the parts Build leaves out — front/back matter and site chrome — so the tree
// build can say what it dropped instead of silently shrinking a document. Empty parts (no title,
// no text) are not reported: there was nothing to drop.
func DroppedTitles(parts []entities.Part) []string {
	var out []string
	for _, s := range parts {
		if strings.TrimSpace(s.Text) == "" && strings.TrimSpace(s.Title) == "" {
			continue
		}
		if IsBoilerplate(s) || IsMatter(s) || IsPageMatter(s) {
			out = append(out, strings.TrimSpace(s.Title))
		}
	}
	return out
}
