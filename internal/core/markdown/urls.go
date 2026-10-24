package markdown

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	mdLinkURL  = regexp.MustCompile(`\]\((https?://[^)\s]+)\)`)
	angleURL   = regexp.MustCompile(`<(https?://[^>\s]+)>`)
	bareURL    = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	inlineCode = regexp.MustCompile("`[^`]*`")
)

// URLs extracts external http(s) URLs from a Markdown body. It is deliberately a parser of the
// AUTHORED page, not a generic link checker:
//
//   - fenced code blocks and inline code are ignored (an example URL is not a source);
//   - Markdown link destinations, autolinks and bare URLs are included;
//   - the order is first-seen, and duplicates collapse.
//
// A wiki page has no other way to declare an external source: the URL is the declaration, and the
// source is created/linked from it.
func URLs(body string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		raw = cleanURL(raw)
		if raw == "" || seen[raw] {
			return
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return
		}
		seen[raw] = true
		out = append(out, raw)
	}

	inFence := false
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// Remove inline code before looking for links; a URL in a code span is an illustration.
		line = inlineCode.ReplaceAllString(line, " ")
		for _, m := range mdLinkURL.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
		for _, m := range angleURL.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
		for _, m := range bareURL.FindAllString(line, -1) {
			add(m)
		}
	}
	return out
}

func cleanURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ".,;:!?")
	// A bare URL at the end of a Markdown list often carries the closing parenthesis or bracket of
	// the prose. Trim only unbalanced trailing delimiters, not a real path segment.
	for len(raw) > 0 {
		last := raw[len(raw)-1]
		if last != ')' && last != ']' && last != '}' {
			break
		}
		open, close := byte('('), byte(')')
		switch last {
		case ']':
			open, close = '[', ']'
		case '}':
			open, close = '{', '}'
		}
		if strings.Count(raw, string(open)) >= strings.Count(raw, string(close)) {
			break
		}
		raw = raw[:len(raw)-1]
	}
	return raw
}
