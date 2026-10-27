package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tags extracts inline #tags (nested with /), ignoring code fences and inline code
// and skipping Markdown headings (a leading "# "). Tags are normalized to ASCII.
func Tags(body string) []string {
	var out []string
	seen := map[string]bool{}
	inFence := false
	inInline := false

	for i := 0; i < len(body); {
		switch {
		case !inInline && strings.HasPrefix(body[i:], "```"):
			inFence = !inFence
			i += 3
			continue
		case !inFence && body[i] == '`':
			inInline = !inInline
			i++
			continue
		case body[i] == '\n':
			// A heading is "# " at the start of a line; skip it.
			j := i + 1
			for j < len(body) && body[j] == '#' {
				j++
			}
			if j > i+1 && j < len(body) && body[j] == ' ' {
				i = j
				continue
			}
			i++
			continue
		}
		if inFence || inInline || body[i] != '#' {
			i++
			continue
		}
		// inline tag: # followed by a letter/digit (rune-aware, so umlauts work)
		j := i + 1
		for j < len(body) {
			r, size := utf8.DecodeRuneInString(body[j:])
			if !isTagRune(r) {
				break
			}
			j += size
		}
		if j > i+1 {
			tag := NormalizeTag(body[i+1 : j])
			if tag != "" && !seen[tag] {
				seen[tag] = true
				out = append(out, tag)
			}
			i = j
			continue
		}
		i++
	}
	return out
}

func isTagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '/'
}
