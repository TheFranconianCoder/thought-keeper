package tree

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// The RENDERING is the static, readable picture of a captured source: what step 1 writes next to
// the object (sources/<ab>/<cd>/<hash>.md) so the document can be read again and again — with
// no tool installed and without re-running an extractor.
//
// It is not only for reading. Every part carries ONE marker line that holds everything the
// outline needs, and the heading line after it carries the title:
//
//	<!--part level=1 page=0 ocr=false-->
//
//	# 1 Introduction
//
//	A program is a sequence of steps.
//
// The level in the marker is the authoritative one (the heading only renders it, one '#' at least),
// so a part that came from "text before the first heading" (level 0) survives the round trip
// unchanged — and a rebuild that parses the rendering produces exactly the parts the bytes
// produced. That is what makes step 2 a READER: no extraction, no OCR, nothing that could make a
// rebuild disagree with the run that created the derivation.
//
// A heading line inside the text is never a heading: only the line directly after a marker is.

// partMarker is the one line that starts a part. It reads BOTH spellings: a vault captured before the
// word was cleaned up has `<!--section ...-->` in its renderings, and a reader that only knew the new
// one would parse those documents as one untitled part.
var partMarker = regexp.MustCompile(`^<!--(?:part|section)\s+(.*?)-->\s*$`)

// Render turns extracted parts into the Markdown rendering (see the package comment above).
func Render(parts []entities.Part) string {
	var b strings.Builder
	for _, s := range parts {
		text := strings.TrimRight(s.Text, " \t\r\n")
		title := strings.TrimSpace(s.Title)
		if strings.TrimSpace(text) == "" && title == "" {
			continue
		}
		level := min(max(s.Level, 0), 6)
		attrs := "level=" + strconv.Itoa(level) + " page=" + strconv.Itoa(s.Page)
		if s.OCR {
			attrs += " ocr=true"
		}
		b.WriteString("<!--part " + attrs + "-->\n\n")
		// The heading line is always written, even for a part without a title: the slot must be
		// there, or the first line of the text would be read as the title.
		hashes := max(level, 1)
		b.WriteString(strings.Repeat("#", hashes))
		if title != "" {
			b.WriteString(" " + title)
		}
		b.WriteString("\n\n")
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// ParseRendering reads a rendering back into parts — the inverse of Render, exact for the fields
// the outline uses (level, title, text, page, ocr). It exists so step 2 never has to extract: a tree
// is rebuilt from the capture artefact.
func ParseRendering(md string) []entities.Part {
	var out []entities.Part
	var (
		cur      *entities.Part
		body     []string
		bodyAt   int // where the body starts, in the ORIGINAL bytes
		pos      int // the offset the next line starts at
		inTitle  bool
		sawTitle bool
	)
	flush := func() {
		if cur == nil {
			return
		}
		// The range describes the TRIMMED text, so slicing the file by it gives exactly what a node
		// carries. The body is joined with "\n" over lines that kept their own content (a trailing \r
		// included), so the join reproduces the bytes of the file.
		raw := strings.Join(body, "\n")
		text := strings.TrimSpace(raw)
		cur.Text = text
		if text == "" {
			cur.TextStart, cur.TextEnd = 0, 0
		} else {
			lead := strings.Index(raw, text)
			cur.TextStart = bodyAt + lead
			cur.TextEnd = bodyAt + lead + len(text)
		}
		out = append(out, *cur)
		cur, body, bodyAt, inTitle, sawTitle = nil, nil, 0, false, false
	}

	for _, raw := range strings.SplitAfter(md, "\n") {
		line := strings.TrimRight(raw, "\r\n")
		next := pos + len(raw)
		if len(body) == 0 {
			bodyAt = pos
		}
		pos = next
		if m := partMarker.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			flush()
			cur = &entities.Part{}
			for kv := range strings.FieldsSeq(m[1]) {
				key, value, found := strings.Cut(kv, "=")
				if !found {
					continue
				}
				switch key {
				case "level":
					if n, err := strconv.Atoi(value); err == nil {
						cur.Level = n
					}
				case "page":
					if n, err := strconv.Atoi(value); err == nil {
						cur.Page = n
					}
				case "ocr":
					cur.OCR = value == "true"
				}
			}
			inTitle = true
			continue
		}
		if cur == nil {
			// Text before the first marker (a hand-written file, a truncated one): keep it as a
			// part without a title instead of dropping what the document says.
			cur = &entities.Part{}
			sawTitle = true
			bodyAt = pos - len(raw)
		}
		if inTitle {
			if strings.TrimSpace(line) == "" && !sawTitle {
				continue // the blank line between the marker and the heading
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				cur.Title = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
				inTitle, sawTitle = false, true
				continue
			}
			// No heading where one was expected: the slot was empty, so this is already the body.
			inTitle, sawTitle = false, true
			body = append(body, strings.TrimSuffix(raw, "\n"))
			continue
		}
		body = append(body, strings.TrimSuffix(raw, "\n"))
	}
	flush()
	return out
}

// Locate fills each part's REFERENCE: where its text sits in the readable text the parts were
// read from. It exists for the producers that do not know — the extractors hand back texts, not
// positions — and it works because the parts come in DOCUMENT ORDER: one forward scan finds each
// text at or after the end of the previous one, so a repeated sentence cannot be matched twice.
//
// A text that is NOT in the readable text (an extractor that normalised it, or a source whose
// readable form is not the object) is left without a reference, and the count says how many. The
// outline then has to carry those texts: a node nobody can reference must not lose its content.
func Locate(text string, parts []entities.Part) (unlocated int) {
	at := 0
	for i := range parts {
		s := &parts[i]
		if s.Text == "" {
			continue
		}
		if s.TextEnd > s.TextStart {
			at = s.TextEnd // already located (the rendering parser knows its own positions)
			continue
		}
		if at > len(text) {
			unlocated++
			continue
		}
		j := strings.Index(text[at:], s.Text)
		if j < 0 {
			unlocated++
			continue
		}
		s.TextStart = at + j
		s.TextEnd = s.TextStart + len(s.Text)
		at = s.TextEnd
	}
	return unlocated
}
