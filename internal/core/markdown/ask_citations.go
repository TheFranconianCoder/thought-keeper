package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	askClaimCitation     = regexp.MustCompile(`\[\[claim:([^\]|]+)(?:\|([^\]]+))?\]\]`)
	askClaimMarkdownLink = regexp.MustCompile(`\[((?:\\.|[^\]])*)\]\(#/claims/([0-9A-Za-z]+)\)`)
	askSourcesLine       = regexp.MustCompile(`^\s*(?:\*\*)?sources(?:\*\*)?:?`)
)

// NormalizeAskAnswer turns the model's [[claim:<id>|label]] citations into numbered references.
//
// A claim is EVIDENCE, not a phrase of the answer: pasting its text into the middle of a sentence
// produces a half sentence. Prose therefore carries a small reference marker — `([1](#cite-1))`
// with the round parentheses as plain prose punctuation — and the numbered Sources block at the end
// carries the claim text. The marker behaves like a footnote: it points at its bibliography entry,
// and that entry carries the actual link to the claim.
//
// Labels are collected from the prose and from the model's own Sources block. The block is rebuilt
// from every cited claim (not only the additional ones), because a numbered reference needs its
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
	// A claim listed only under the model's Sources block still needs an entry.
	if sourcesAt >= 0 {
		for i := sourcesAt + 1; i < len(lines); i++ {
			for _, id := range claimIDsInLine(lines[i]) {
				_ = number(id)
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
	for _, id := range order {
		label := labels[id]
		if label == "" {
			label = fallbackCitationLabel(id)
		}
		fmt.Fprintf(&out, "%d. [%s](#/claims/%s)\n", numbers[id], escapeCitationLabel(label), id)
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
		n := number(m[2])
		if n == "" {
			return ""
		}
		return fmt.Sprintf("[%s](#cite-%s)", n, n)
	})
	return replaceCitationMatches(line, askClaimCitation, func(m []string) string {
		if len(m) < 2 {
			return ""
		}
		id := strings.TrimSpace(m[1])
		n := number(id)
		if n == "" {
			return ""
		}
		return fmt.Sprintf("[%s](#cite-%s)", n, n)
	})
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

// claimIDsInLine lists the claim addresses a line cites, in first-appearance order.
func claimIDsInLine(line string) []string {
	var ids []string
	for _, m := range askClaimMarkdownLink.FindAllStringSubmatch(line, -1) {
		if len(m) > 2 {
			ids = append(ids, m[2])
		}
	}
	for _, m := range askClaimCitation.FindAllStringSubmatch(line, -1) {
		if len(m) > 1 {
			ids = append(ids, strings.TrimSpace(m[1]))
		}
	}
	return ids
}

// askCitationLabels collects the short claim text that appears anywhere in the answer, so a numbered
// reference can name its claim in the bibliography.
func askCitationLabels(lines []string) map[string]string {
	labels := make(map[string]string)
	for _, line := range lines {
		for _, m := range askClaimCitation.FindAllStringSubmatch(line, -1) {
			if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
				labels[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
			}
		}
		for _, m := range askClaimMarkdownLink.FindAllStringSubmatch(line, -1) {
			label, _ := markdownCitationLabel(m[1])
			if label == "" || strings.EqualFold(label, "claim") || isNumber(label) {
				continue
			}
			if _, seen := labels[m[2]]; !seen {
				labels[m[2]] = label
			}
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

func fallbackCitationLabel(id string) string {
	if id == "" {
		return "claim"
	}
	return id
}

func unescapeCitationLabel(s string) string {
	return strings.NewReplacer(`\[`, "[", `\]`, "]").Replace(s)
}

func escapeCitationLabel(s string) string {
	return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s)
}
