package extract

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// Headings in a PDF are not marked up: `pdftotext` hands over lines, and where a chapter starts is
// invisible in them. Without a heuristic every page becomes ONE node titled "Page 42" — an outline
// that describes nothing, and work units larger than they need to be. The heuristic below is
// deterministic and conservative: it only ever splits a page into more parts and never merges
// across pages, so a PDF without recognisable headings keeps exactly the page-per-node shape it had.
// `model_compile` re-titles and summarises the nodes afterwards, so a mediocre guess costs a better
// starting point, never knowledge — no text is dropped either way.

const (
	// A heading is a label, not a sentence.
	headingMaxChars = 80
	headingMaxWords = 12
	// headingCapDepth bounds the depth numbering may claim (1.2.3 is a level, 1.2.3.4.5 is a line).
	headingCapDepth = 4
)

var (
	// "1 Introduction" · "2.3 The memory model" · "3.1.4 Detail"
	numberedHeading = regexp.MustCompile(`^\s*([0-9]{1,3}(?:\.[0-9]{1,3}){0,5})[.)]?\s+(\S.*)$`)
	// "Chapter 3" · "Kapitel 3.2 Cache" · "Appendix B" · "Teil II"
	wordedHeading = regexp.MustCompile(`(?i)^\s*(chapter|kapitel|part|abschnitt|part|teil|appendix|anhang|book|buch)\s+(?:[0-9]{1,3}(?:\.[0-9]{1,3})*|[ivxlcdm]{1,7}|[a-z])\b\.?\s*(.*)$`)
)

// pageText is one page of a PDF with the lines that survived the furniture filter.
type pageText struct {
	no    int
	ocr   bool
	lines []string
}

// headingCandidate reports whether a line looks like a heading, and at which level. It is pure, so
// the shape a document gets is reproducible from its text alone.
//
// Three shapes are recognised: numbering ("2.3 Title" → level 2), a chapter word
// ("Kapitel 3 Cache" → level 1), and an all-caps line of at least two words ("RESULTS AND
// DISCUSSION" → level 2, below a numbered chapter when there is one).
func headingCandidate(line string) (level int, title string, ok bool) {
	t := strings.TrimSpace(line)
	if t == "" || len([]rune(t)) > headingMaxChars || len(strings.Fields(t)) > headingMaxWords {
		return 0, "", false
	}
	// A heading does not end in the punctuation of a sentence.
	if strings.ContainsAny(t[len(t)-1:], ".,;:!?…") {
		return 0, "", false
	}
	if wordedHeading.MatchString(t) {
		return 1, t, true
	}
	if m := numberedHeading.FindStringSubmatch(t); m != nil {
		// The text after the number must start a phrase, not a sentence: "2.3 The cache" is a
		// heading, "5.5 is the score" is a line of prose that happens to begin with numbers.
		if !startsUpper(m[2]) {
			return 0, "", false
		}
		level := min(1+strings.Count(m[1], "."), headingCapDepth)
		return level, t, true
	}
	if isUpperCaseHeading(t) {
		return 2, t, true
	}
	return 0, "", false
}

// startsUpper reports whether the first letter of a phrase is not lowercase (a heading starts a
// phrase; scripts without letter case pass, so non-Latin documents are not excluded).
func startsUpper(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return !unicode.IsLower(r)
		}
	}
	return false
}

// isUpperCaseHeading reports whether a line is an all-caps label: several words, or one word long
// enough to be a heading ("CONCLUSION", not "TCP").
func isUpperCaseHeading(t string) bool {
	words := strings.Fields(t)
	letters := 0
	for _, r := range t {
		if !unicode.IsLetter(r) {
			continue
		}
		if unicode.IsLower(r) {
			return false
		}
		letters++
	}
	if letters < 4 {
		return false
	}
	if len(words) >= 2 {
		return true
	}
	return len([]rune(strings.Join(words, ""))) >= 6
}

// partsFromPages turns the pages into ordered parts: a page is split at its heading
// candidates, and the text before the first one (or the whole page, when it has none) becomes a
// part of its own titled "Page N" — the shape a PDF without detectable structure always had.
//
// No part crosses a page boundary, so the work unit stays a part a model can finish in one
// answer, and a page that came out empty (a scan OCR could not read) still appears in the outline
// instead of disappearing from it.
func partsFromPages(pages []pageText) []entities.Part {
	var out []entities.Part
	for _, p := range pages {
		added := len(out)
		var cur *entities.Part
		flush := func() {
			if cur == nil {
				return
			}
			cur.Text = strings.TrimSpace(cur.Text)
			if cur.Text != "" || cur.Title != "" {
				out = append(out, *cur)
			}
			cur = nil
		}
		for _, line := range p.lines {
			if level, title, ok := headingCandidate(line); ok {
				flush()
				cur = &entities.Part{Level: level, Title: title, Page: p.no, OCR: p.ocr}
				continue
			}
			if cur == nil {
				cur = &entities.Part{
					Level: 1, Title: fmt.Sprintf("Page %d", p.no), Page: p.no, OCR: p.ocr,
				}
			}
			cur.Text += line + "\n"
		}
		flush()
		if len(out) == added {
			// The page carried nothing but furniture: keep it visible as a gap in the outline.
			out = append(out, entities.Part{
				Level: 1, Title: fmt.Sprintf("Page %d", p.no), Page: p.no, OCR: p.ocr,
			})
		}
	}
	return out
}
