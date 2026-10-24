package extract

import (
	"regexp"
	"strings"
)

// Page furniture is everything on a page that is not the document's text: the running head, the
// running foot, the page number, a "Page 42" label, a decorative rule. `pdftotext -layout` hands it
// all over in the text, and without a filter it becomes part of a source node — and therefore a
// claim ("this chapter is called X", "the page carries the number 42"). That is metadata used as
// page content, which is exactly what it must not be.
//
// The filter is deliberately conservative and page-local:
//
//   - a line that is nothing but a number (with the punctuation page numbers come wrapped in) is
//     furniture anywhere on the page;
//   - a line that repeats in the top or bottom band of at least three pages is the running head/foot;
//   - the middle of a page is never touched beyond the number rule.
//
// The text that is kept is returned unchanged, so a page's prose survives verbatim.

// furnitureBands is how many of the first/last non-empty lines of a page may be running heads/feet.
const furnitureBands = 2

// furnitureMinPages and furnitureShare decide when a repeated band line is furniture: it has to
// appear on this many pages AND on at least this share of them. A line that happens to repeat in a
// two-page document, or a single stray heading, therefore stays where it is.
const (
	furnitureMinPages = 3
	furnitureShare    = 0.5
)

// furnitureMaxChars bounds the repetition rule: a running head is a short label, so a long line is
// never dropped for repeating (prose that repeats is prose — a refrain, a licence notice — and it is
// the document's text).
const furnitureMaxChars = 80

var (
	// 42 · - 42 - · [42] · | 42 | · 42. · 42 / 500 · 42|500
	numberLine = regexp.MustCompile(`^[\s\-–—–_\(\[\|]*[0-9]{1,4}[\s\-–—_\)\]\|]*(?:[\/|]\s*[0-9]{1,4})?[\s\-–—–_\.\)\]\|]*$`)
	// Page 42 · page 42 of 500 · Seite 42 · Seite 42 von 500
	pageWordLine = regexp.MustCompile(`(?i)^\s*(?:page|seite)\s+[0-9]{1,4}\s*(?:of|von|/)\s*[0-9]{1,4}\s*$|^\s*(?:page|seite)\s+[0-9]{1,4}\s*$`)
	// xii · IV · - xii -   (only in the band, and only with a real roman numeral character, so a
	// unit like "mm" or "cc" is never touched)
	romanLine = regexp.MustCompile(`^[\s\-–—–_\(\[\|]*[ivxlcdmIVXLCDM]{2,7}[\s\-–—_\.\)\]\|]*$`)
	romanMark = regexp.MustCompile(`[ivxIVX]`)
	// A decorative rule: only punctuation-ish characters.
	ruleLine = regexp.MustCompile(`^[\s\-–—–_=*•·⋅~\+#]{3,}$`)
	digitRun = regexp.MustCompile(`[0-9]+`)
)

// stripPageFurniture removes page numbers, decorative rules and repeated running heads/feet from a
// document that has been split into pages of lines. It returns the kept lines per page and how many
// lines it dropped in total.
func stripPageFurniture(pages [][]string) ([][]string, int) {
	repeated := repeatedBandLines(pages)
	out := make([][]string, len(pages))
	dropped := 0
	for i, lines := range pages {
		band := bandIndexes(lines)
		kept := make([]string, 0, len(lines))
		for idx, line := range lines {
			_, inBand := band[idx]
			key := furnitureKey(line)
			if isFurnitureLine(line, inBand) || (inBand && key != "" && repeated[key]) {
				dropped++
				continue
			}
			kept = append(kept, line)
		}
		out[i] = kept
	}
	return out, dropped
}

// bandIndexes is the set of line indexes in the top/bottom band of a page, counted over its
// non-empty lines (a running head is the first thing on the page, even after blank lines).
func bandIndexes(lines []string) map[int]struct{} {
	var nonEmpty []int
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, i)
		}
	}
	out := make(map[int]struct{}, furnitureBands*2)
	for i := 0; i < len(nonEmpty) && i < furnitureBands; i++ {
		out[nonEmpty[i]] = struct{}{}
	}
	for i := len(nonEmpty) - furnitureBands; i < len(nonEmpty); i++ {
		if i >= 0 {
			out[nonEmpty[i]] = struct{}{}
		}
	}
	return out
}

// repeatedBandLines counts the normalized band lines of every page and reports the ones that repeat
// often enough to be furniture rather than text.
func repeatedBandLines(pages [][]string) map[string]bool {
	counts := make(map[string]int, len(pages)*2)
	for _, lines := range pages {
		band := bandIndexes(lines)
		seen := make(map[string]bool, len(band))
		for idx := range band {
			if len(strings.TrimSpace(lines[idx])) > furnitureMaxChars {
				continue // too long to be a running head: it is the document's text
			}
			key := furnitureKey(lines[idx])
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			counts[key]++
		}
	}
	min := max(furnitureMinPages, 3)
	repeated := make(map[string]bool)
	for key, n := range counts {
		if n >= min && float64(n) >= furnitureShare*float64(len(pages)) {
			repeated[key] = true
		}
	}
	return repeated
}

// isFurnitureLine reports whether one line is furniture. inBand says whether the line sits in the
// top/bottom band of its page — the number rule applies everywhere, the roman-numeral rule (which
// could match a unit like "mm") only there.
func isFurnitureLine(line string, inBand bool) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case pageWordLine.MatchString(t):
		return true
	case numberLine.MatchString(t):
		return true
	case ruleLine.MatchString(t):
		return true
	case inBand && romanLine.MatchString(t) && romanMark.MatchString(t):
		return true
	}
	return false
}

// furnitureKey normalizes a line so a running head with a changing page number still matches itself:
// "Kapitel 3 · Nebenläufigkeit   42" and "Kapitel 3 · Nebenläufigkeit   43" share a key. Whitespace
// collapses, digits become a placeholder, and the surrounding punctuation page numbers come in is
// dropped.
func furnitureKey(line string) string {
	t := strings.ToLower(strings.TrimSpace(line))
	if t == "" {
		return ""
	}
	t = digitRun.ReplaceAllString(t, "#")
	t = strings.Join(strings.Fields(t), " ")
	t = strings.Trim(t, "-–—–_|[](){}.:;,*•·")
	t = strings.TrimSpace(t)
	if t == "" || t == "#" {
		return "" // a pure number: handled by the number rule, never a "repeated line"
	}
	return t
}
