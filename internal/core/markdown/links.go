// Package markdown holds pure (stdlib-only) Markdown semantics: wikilinks and tags.
package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// LinkRef is a parsed wikilink occurrence.
type LinkRef struct {
	RawTarget string
	Alias     string
	Heading   string
	Position  int
}

// Wikilinks extracts [[target]], [[target|alias]], [[target#heading]] and
// [[target#heading|alias]] occurrences, ignoring fenced and inline code. Duplicate targets within a
// body are returned once (first position wins) so they map to one edge.
func Wikilinks(body string) []LinkRef {
	var out []LinkRef
	seen := map[string]bool{}
	var fenceChar byte
	fenceLen := 0
	inlineTicks := 0

	for i := 0; i < len(body); {
		if fenceLen == 0 && inlineTicks == 0 && (body[i] == '`' || body[i] == '~') {
			n := runLen(body, i, body[i])
			if n >= 3 && lineStart(body, i) {
				fenceChar, fenceLen = body[i], n
				i += n
				continue
			}
		}
		if fenceLen > 0 {
			if body[i] == fenceChar && lineStart(body, i) {
				if n := runLen(body, i, body[i]); n >= fenceLen {
					fenceChar, fenceLen = 0, 0
					i += n
					continue
				}
			}
			i++
			continue
		}
		if body[i] == '`' {
			n := runLen(body, i, body[i])
			switch {
			case inlineTicks == 0:
				inlineTicks = n
			case n == inlineTicks:
				inlineTicks = 0
			}
			i += n
			continue
		}
		if inlineTicks > 0 {
			i++
			continue
		}
		if !strings.HasPrefix(body[i:], "[[") {
			i++
			continue
		}
		end := strings.Index(body[i+2:], "]]")
		if end < 0 {
			i++
			continue
		}
		inner := body[i+2 : i+2+end]
		ref := parseLink(inner, i)
		spanEnd := i + 2 + end + 2
		if ref.RawTarget == "" || seen[ref.RawTarget] {
			i = spanEnd
			continue
		}
		seen[ref.RawTarget] = true
		out = append(out, ref)
		i = spanEnd
	}
	return out
}

// runLen counts a run of one byte at i.
func runLen(body string, i int, b byte) int {
	n := 0
	for i+n < len(body) && body[i+n] == b {
		n++
	}
	return n
}

// lineStart reports whether i starts a line, allowing up to three leading spaces (Markdown's
// fenced-code rule).
func lineStart(body string, i int) bool {
	j := i
	for spaces := 0; j > 0 && body[j-1] == ' ' && spaces < 3; spaces++ {
		j--
	}
	return j == 0 || body[j-1] == '\n'
}

func parseLink(inner string, pos int) LinkRef {
	ref := LinkRef{Position: pos}
	if idx := strings.IndexByte(inner, '|'); idx >= 0 {
		ref.Alias = strings.TrimSpace(inner[idx+1:])
		inner = inner[:idx]
	}
	if idx := strings.IndexByte(inner, '#'); idx >= 0 {
		ref.Heading = strings.TrimSpace(inner[idx+1:])
		inner = inner[:idx]
	}
	ref.RawTarget = strings.TrimSpace(inner)
	// [[#Heading]] has an empty target and is not a link edge. Neither is syntax that merely looks
	// like a wikilink: C++ attributes (`[[nodiscard]]`), SQL bracket grammar and shell tests
	// (`[[ ! -d "$OUT" ]]`) are prose in a claim body, not links to a claim.
	if ref.RawTarget == "" || isCodeLikeTarget(ref.RawTarget) {
		return LinkRef{}
	}
	return ref
}

// cppAttributes are C++ standard attributes. They are written exactly like a wikilink, so the only
// way to tell them apart from a claim reference is to know the names.
var cppAttributes = map[string]bool{
	"maybe_unused": true, "nodiscard": true, "deprecated": true, "fallthrough": true,
	"no_unique_address": true, "likely": true, "unlikely": true, "noreturn": true,
	"carries_dependency": true, "assume": true, "alloc_size": true, "alloc_align": true,
	"expects": true, "ensures": true, "gnu": true, "clang": true,
}

// isCodeLikeTarget reports whether a bracketed fragment is code syntax rather than a claim link.
// It is deliberately conservative: a target with code punctuation, a known C++ attribute, or a bare
// snake_case identifier is syntax; anything else is treated as a link so a genuine red link survives.
func isCodeLikeTarget(target string) bool {
	if strings.HasPrefix(target, "!") || strings.ContainsAny(target, "[]{}\"`\\\n\t") {
		return true
	}
	lower := strings.ToLower(target)
	if cppAttributes[lower] {
		return true
	}
	return !strings.ContainsAny(target, " \t") && strings.Contains(target, "_")
}

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

// Unlink replaces wikilinks whose target is unknown with plain text (keeping the
// alias/display text). It is the deterministic fallback that guarantees a cleanup
// leaves no ghost link behind. Code fences and inline code are left untouched.
func Unlink(body string, known func(target string) bool) (string, int) {
	var b strings.Builder
	b.Grow(len(body))
	changed := 0
	flushed := 0
	fence := false
	inline := false

	for i := 0; i < len(body); {
		switch {
		case !inline && strings.HasPrefix(body[i:], "```"):
			fence = !fence
			i += 3
			continue
		case !fence && body[i] == '`':
			inline = !inline
			i++
			continue
		}
		if fence || inline || !strings.HasPrefix(body[i:], "[[") {
			i++
			continue
		}
		end := strings.Index(body[i+2:], "]]")
		if end < 0 {
			break
		}
		spanEnd := i + 2 + end + 2
		ref := parseLink(body[i+2:i+2+end], i)
		if ref.RawTarget != "" && !known(ref.RawTarget) {
			b.WriteString(body[flushed:i])
			display := ref.Alias
			if display == "" {
				display = ref.RawTarget
			}
			b.WriteString(display)
			changed++
			flushed = spanEnd
		}
		i = spanEnd
	}
	if changed == 0 {
		return body, 0
	}
	b.WriteString(body[flushed:])
	return b.String(), changed
}

// LinkKey normalizes a page title or link target for fuzzy matching: lowercased,
// transliterated to ASCII, every run of non-alphanumerics collapsed to one space. It
// makes "Cache-Invalidation", "cache invalidation" and "Cache invalidation." compare
// equal, so a link survives the small spelling drift of a re-derivation.
func LinkKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		ascii := asciiOf(r)
		if ascii == "" {
			if b.Len() > 0 && !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteString(ascii)
		space = false
	}
	return strings.TrimRight(b.String(), " ")
}

// Retarget rewrites wikilinks whose target matches from — compared by LinkKey, so
// case, punctuation and umlauts may differ — to point at to, keeping the heading and
// the alias. It is how a link survives the page it points at being re-derived under a
// slightly different title. Code fences and inline code are left untouched.
func Retarget(body, from, to string) (string, int) {
	key := LinkKey(from)
	if key == "" || strings.TrimSpace(to) == "" {
		return body, 0
	}
	var b strings.Builder
	b.Grow(len(body))
	changed := 0
	flushed := 0
	fence := false
	inline := false

	for i := 0; i < len(body); {
		switch {
		case !inline && strings.HasPrefix(body[i:], "```"):
			fence = !fence
			i += 3
			continue
		case !fence && body[i] == '`':
			inline = !inline
			i++
			continue
		}
		if fence || inline || !strings.HasPrefix(body[i:], "[[") {
			i++
			continue
		}
		end := strings.Index(body[i+2:], "]]")
		if end < 0 {
			break
		}
		spanEnd := i + 2 + end + 2
		ref := parseLink(body[i+2:i+2+end], i)
		if ref.RawTarget != "" && LinkKey(ref.RawTarget) == key {
			b.WriteString(body[flushed:i])
			b.WriteString("[[")
			b.WriteString(strings.TrimSpace(to))
			if ref.Heading != "" {
				b.WriteString("#")
				b.WriteString(ref.Heading)
			}
			if ref.Alias != "" {
				b.WriteString("|")
				b.WriteString(ref.Alias)
			}
			b.WriteString("]]")
			changed++
			flushed = spanEnd
		}
		i = spanEnd
	}
	if changed == 0 {
		return body, 0
	}
	b.WriteString(body[flushed:])
	return b.String(), changed
}

// StripTrailingTags removes trailing lines that consist only of tags (e.g. a
// hand-written "#caching #correctness" footer). Tags belong in frontmatter, not at
// the end of the prose.
func StripTrailingTags(body string) string {
	lines := strings.Split(body, "\n")
	end := len(lines)
	for end > 0 {
		line := strings.TrimSpace(lines[end-1])
		if line == "" {
			end--
			continue
		}
		if isTagOnlyLine(line) {
			end--
			continue
		}
		break
	}
	return strings.TrimRight(strings.Join(lines[:end], "\n"), "\n ")
}

func isTagOnlyLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if len(f) < 2 || f[0] != '#' {
			return false
		}
		rest := f[1:]
		for i := 0; i < len(rest); {
			r, size := utf8.DecodeRuneInString(rest[i:])
			if !isTagRune(r) {
				return false
			}
			i += size
		}
	}
	return true
}
