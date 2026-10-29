package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	askClaimCitation      = regexp.MustCompile(`\[\[claim:([^\]|]+)(?:\|([^\]]+))?\]\]`)
	askSourceCitation     = regexp.MustCompile(`\[\[source:([^\]|]+)(?:\|([^\]]+))?\]\]`)
	askClaimMarkdownLink  = regexp.MustCompile(`\[((?:\\.|[^\]])*)\]\(#/claims/([0-9A-Za-z]+)\)`)
	askSourceMarkdownLink = regexp.MustCompile(`\[((?:\\.|[^\]])*)\]\(#/sources/([0-9A-Za-z]+)\)`)
	askBareWikilink       = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	askSourcesLine        = regexp.MustCompile(`^\s*(?:\*\*)?sources(?:\*\*)?:?`)
)

// The two things an answer may cite. The key that numbers and labels them is prefixed, so a claim
// id and a source hash can never collide (they could not anyway, but the code should not rely on
// the shape of an id to stay honest).
const (
	citeClaim  = "claim"
	citeSource = "source"
)

func citationKey(kind, id string) string { return kind + ":" + strings.TrimSpace(id) }

func splitCitationKey(key string) (kind, id string) {
	kind, id, ok := strings.Cut(key, ":")
	if !ok {
		return citeClaim, key
	}
	return kind, id
}

// NormalizeAskAnswer turns the model's [[claim:<id>|label]] and [[source:<hash>|title]] citations
// into numbered references.
//
// A claim (or a source's overview) is EVIDENCE, not a phrase of the answer: pasting its text into
// the middle of a sentence produces a half sentence. Prose therefore carries a small reference
// marker — `([1](#cite-1))` with the round parentheses as plain prose punctuation — and the numbered
// Sources block at the end carries the label. The marker behaves like a footnote: it points at its
// bibliography entry, and that entry carries the actual link to the claim or the source page.
//
// Labels are collected from the prose and from the model's own Sources block. The block is rebuilt
// from everything cited (not only the additional entries), because a numbered reference needs its
// bibliography. A missing block is added; an empty one disappears.
func NormalizeAskAnswer(answer string) string {
	lines := strings.Split(answer, "\n")
	labels := askCitationLabels(lines)

	sourcesAt := -1
	for i, line := range lines {
		if askSourcesLine.MatchString(strings.ToLower(strings.TrimSpace(line))) {
			sourcesAt = i
			break
		}
	}
	headEnd := len(lines)
	if sourcesAt >= 0 {
		headEnd = sourcesAt
	}

	numbers := map[string]int{}
	var order []string
	number := func(id string) string {
		if id == "" {
			return ""
		}
		if n, ok := numbers[id]; ok {
			return strconv.Itoa(n)
		}
		n := len(order) + 1
		numbers[id] = n
		order = append(order, id)
		return strconv.Itoa(n)
	}

	for i := 0; i < headEnd; i++ {
		lines[i] = replaceProseCitations(lines[i], number)
	}
	// A claim or source listed only under the model's Sources block still needs an entry.
	if sourcesAt >= 0 {
		for i := sourcesAt + 1; i < len(lines); i++ {
			for _, key := range citationKeysInLine(lines[i]) {
				_ = number(key)
			}
		}
	}

	prose := strings.TrimRight(strings.Join(lines[:headEnd], "\n"), "\n")
	if len(order) == 0 {
		return prose
	}

	var out strings.Builder
	if prose != "" {
		out.WriteString(prose)
		out.WriteString("\n\n")
	}
	out.WriteString("Sources:\n")
	for _, key := range order {
		label := labels[key]
		if label == "" {
			label = fallbackCitationLabel(key)
		}
		kind, id := splitCitationKey(key)
		href := "#/claims/" + id
		if kind == citeSource {
			href = "#/sources/" + id
		}
		fmt.Fprintf(&out, "%d. [%s](%s)\n", numbers[key], escapeCitationLabel(label), href)
	}
	return strings.TrimRight(out.String(), "\n")
}

// replaceProseCitations rewrites one line's claim links into numbered reference markers. The marker
// is wrapped in round parentheses unless it already sits inside a pair, so `Prose ([[claim:…]])`
// stays `Prose ([1](…))` and `Prose [[claim:…]]` becomes `Prose ([1](…))`.
func replaceProseCitations(line string, number func(string) string) string {
	line = replaceCitationMatches(line, askClaimMarkdownLink, func(m []string) string {
		if len(m) < 3 {
			return ""
		}
		return citationMarker(number(citationKey(citeClaim, m[2])))
	})
	line = replaceCitationMatches(line, askSourceMarkdownLink, func(m []string) string {
		if len(m) < 3 {
			return ""
		}
		return citationMarker(number(citationKey(citeSource, m[2])))
	})
	line = replaceCitationMatches(line, askClaimCitation, func(m []string) string {
		if len(m) < 2 {
			return ""
		}
		return citationMarker(number(citationKey(citeClaim, m[1])))
	})
	line = replaceCitationMatches(line, askSourceCitation, func(m []string) string {
		if len(m) < 2 {
			return ""
		}
		return citationMarker(number(citationKey(citeSource, m[1])))
	})
	// A bare [[title]] is not a citation. The UI renders it as a claim finder query, so demote a
	// leftover link to its label instead of letting a model slip produce a misleading search link.
	// A wiki link is left alone: the Ask view can link a page even though the prompt asks for
	// claims and sources.
	return askBareWikilink.ReplaceAllStringFunc(line, func(raw string) string {
		m := askBareWikilink.FindStringSubmatch(raw)
		if len(m) < 2 {
			return raw
		}
		if strings.HasPrefix(strings.TrimSpace(m[1]), "wiki:") {
			return raw
		}
		if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
			return strings.TrimSpace(m[2])
		}
		return strings.TrimSpace(m[1])
	})
}

func citationMarker(n string) string {
	if n == "" {
		return ""
	}
	return fmt.Sprintf("[%s](#cite-%s)", n, n)
}

func replaceCitationMatches(line string, re *regexp.Regexp, build func([]string) string) string {
	matches := re.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return line
	}
	var out strings.Builder
	last := 0
	for _, idx := range matches {
		start, end := idx[0], idx[1]
		out.WriteString(line[last:start])
		raw := line[start:end]
		replacement := build(re.FindStringSubmatch(raw))
		if replacement != "" {
			if !insideParentheses(line, start, end) {
				out.WriteByte('(')
				out.WriteString(replacement)
				out.WriteByte(')')
			} else {
				out.WriteString(replacement)
			}
		}
		last = end
	}
	out.WriteString(line[last:])
	return out.String()
}

func insideParentheses(line string, start, end int) bool {
	return start > 0 && line[start-1] == '(' && end < len(line) && line[end] == ')'
}

// citationKeysInLine lists the claim and source addresses a line cites, in first-appearance order.
func citationKeysInLine(line string) []string {
	var keys []string
	for _, m := range askClaimMarkdownLink.FindAllStringSubmatch(line, -1) {
		if len(m) > 2 {
			keys = append(keys, citationKey(citeClaim, m[2]))
		}
	}
	for _, m := range askSourceMarkdownLink.FindAllStringSubmatch(line, -1) {
		if len(m) > 2 {
			keys = append(keys, citationKey(citeSource, m[2]))
		}
	}
	for _, m := range askClaimCitation.FindAllStringSubmatch(line, -1) {
		if len(m) > 1 {
			keys = append(keys, citationKey(citeClaim, m[1]))
		}
	}
	for _, m := range askSourceCitation.FindAllStringSubmatch(line, -1) {
		if len(m) > 1 {
			keys = append(keys, citationKey(citeSource, m[1]))
		}
	}
	return keys
}

// askCitationLabels collects the short claim text or source title that appears anywhere in the
// answer, so a numbered reference can name it in the bibliography. A label that is already a
// placeholder ("claim", "source") or a bare reference number is not a name.
func askCitationLabels(lines []string) map[string]string {
	labels := make(map[string]string)
	record := func(key, label string, skipNumber bool) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		if _, seen := labels[key]; seen {
			return
		}
		if skipNumber && isNumber(label) {
			return
		}
		labels[key] = label
	}
	for _, line := range lines {
		for _, m := range askClaimCitation.FindAllStringSubmatch(line, -1) {
			if len(m) > 2 {
				record(citationKey(citeClaim, m[1]), m[2], false)
			}
		}
		for _, m := range askSourceCitation.FindAllStringSubmatch(line, -1) {
			if len(m) > 2 {
				record(citationKey(citeSource, m[1]), m[2], false)
			}
		}
		for _, m := range askClaimMarkdownLink.FindAllStringSubmatch(line, -1) {
			label, _ := markdownCitationLabel(m[1])
			if strings.EqualFold(label, "claim") {
				continue
			}
			record(citationKey(citeClaim, m[2]), label, true)
		}
		for _, m := range askSourceMarkdownLink.FindAllStringSubmatch(line, -1) {
			label, _ := markdownCitationLabel(m[1])
			if strings.EqualFold(label, "source") {
				continue
			}
			record(citationKey(citeSource, m[2]), label, false)
		}
	}
	return labels
}

func isNumber(value string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil
}

// markdownCitationLabel reads the label of an already-normalized claim link. A prose citation is
// written as [\[label\]](…), so the outer escaped brackets are syntax, not part of the label.
func markdownCitationLabel(content string) (string, bool) {
	bracketed := strings.HasPrefix(content, `\[`) && strings.HasSuffix(content, `\]`)
	if bracketed {
		content = strings.TrimSuffix(strings.TrimPrefix(content, `\[`), `\]`)
	}
	return unescapeCitationLabel(content), bracketed
}

func fallbackCitationLabel(key string) string {
	kind, id := splitCitationKey(key)
	if id != "" {
		return id
	}
	if kind == citeSource {
		return "source"
	}
	return "claim"
}

func unescapeCitationLabel(s string) string {
	return strings.NewReplacer(`\[`, "[", `\]`, "]").Replace(s)
}

func escapeCitationLabel(s string) string {
	return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s)
}
