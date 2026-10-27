package markdown

import "strings"

// QuoteBracketSyntax wraps every [[…]] span outside code in an inline code mark. A claim body has no
// links: a double-bracket span in claim prose is syntax quoted from a source — a C attribute, a shell
// test, a grammar fragment — and it must read as code. Fenced and inline code are left alone, so the
// pass is idempotent: a span that already sits in code is not touched.
//
// A span that crosses a line is left as it is: inline code cannot span a line, and a multi-line
// fragment was never one code mark.
func QuoteBracketSyntax(body string) (string, int) {
	var (
		b           strings.Builder
		fenceChar   byte
		fenceLen    int
		inlineTicks int
		changes     int
	)
	b.Grow(len(body))

	for i := 0; i < len(body); {
		if fenceLen == 0 && inlineTicks == 0 && (body[i] == '`' || body[i] == '~') {
			if n := runLen(body, i, body[i]); n >= 3 && lineStart(body, i) {
				fenceChar, fenceLen = body[i], n
				b.WriteString(body[i : i+n])
				i += n
				continue
			}
		}
		if fenceLen > 0 {
			if body[i] == fenceChar && lineStart(body, i) {
				if n := runLen(body, i, body[i]); n >= fenceLen {
					fenceChar, fenceLen = 0, 0
					b.WriteString(body[i : i+n])
					i += n
					continue
				}
			}
			b.WriteByte(body[i])
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
			b.WriteString(body[i : i+n])
			i += n
			continue
		}
		if inlineTicks > 0 {
			b.WriteByte(body[i])
			i++
			continue
		}
		if !strings.HasPrefix(body[i:], "[[") {
			b.WriteByte(body[i])
			i++
			continue
		}
		end := strings.Index(body[i+2:], "]]")
		if end < 0 {
			b.WriteByte(body[i])
			i++
			continue
		}
		span := body[i : i+2+end+2]
		// Adjacent backticks would merge two code spans into one fence; a line break cannot live in
		// inline code. Leave both alone rather than write Markdown that means something else.
		adjacentTick := (i > 0 && body[i-1] == '`') || (i+len(span) < len(body) && body[i+len(span)] == '`')
		if adjacentTick || strings.ContainsAny(span, "\r\n") {
			b.WriteString(span)
			i += len(span)
			continue
		}
		ticks := strings.Repeat("`", tickLen(span))
		b.WriteString(ticks)
		b.WriteString(span)
		b.WriteString(ticks)
		changes++
		i += len(span)
	}
	if changes == 0 {
		return body, 0
	}
	return b.String(), changes
}

// tickLen picks a backtick run that cannot close inside span.
func tickLen(span string) int {
	n := 1
	for strings.Contains(span, strings.Repeat("`", n)) {
		n++
	}
	return n
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
