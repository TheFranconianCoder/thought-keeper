// Package textdiff writes the difference between two snapshots of the same page as a unified diff.
//
// It is what makes a refresh visible: the refresher has the old body in hand (it already reads it to
// decide whether the outline has to be rebuilt) and the new one, so a readable record of the change
// costs no extra I/O and no second copy of the bytes.
//
// The diff is LINE based and deliberately not smart: a reflow IS a diff here, even though the
// rebuild keeps the claims of a reflowed part. The record tells the reader what the page says now;
// the claim tolerance is a different question.
package textdiff

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// context is how many unchanged lines surround a hunk. Three is the unified-diff default and
	// enough for a reader to recognize where the change sits.
	context = 3
	// maxOutputLines and maxOutputBytes bound one record. A page that is rewritten end to end would
	// otherwise copy itself into a second file.
	maxOutputLines = 2000
	maxOutputBytes = 256 << 10
	// maxCells bounds the LCS table: 2M cells is a few milliseconds and 8 MiB transient. Beyond it
	// the middle is reported as one replaced block (and the output is truncated anyway).
	maxCells = 2_000_000
)

// Result is one diff.
type Result struct {
	// Text is a unified diff (3 lines of context), empty when the bodies are identical.
	Text string
	// Added and Removed count the changed LINES in the whole change, not only the lines that fit
	// into Text: a truncated diff still says how big the change was.
	Added   int
	Removed int
	// Truncated says the change was larger than the record: Text stops early.
	Truncated bool
}

// Lines diffs two bodies line by line.
func Lines(oldText, newText string) Result {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// A weekly change of a long article is a few lines in the middle: trimming the shared head and
	// tail keeps the LCS small enough to be instant.
	prefix := commonPrefix(oldLines, newLines)
	suffix := commonSuffix(oldLines[prefix:], newLines[prefix:])
	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]

	edits := buildEdits(oldLines, newLines, prefix, suffix, oldMid, newMid)
	res := Result{}
	for _, e := range edits {
		switch e.kind {
		case opDelete:
			res.Removed++
		case opInsert:
			res.Added++
		}
	}
	if res.Added == 0 && res.Removed == 0 {
		return Result{}
	}
	res.Text, res.Truncated = render(edits)
	return res
}

// opKind is one edit's shape.
type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

// edit is one line of the diff: an unchanged line present in both bodies, a line only the old body
// has, or one only the new body has. oldNo/newNo are 1-based line numbers in their body (0 when the
// line does not exist there).
type edit struct {
	kind  opKind
	oldNo int
	newNo int
	text  string
}

// buildEdits assembles the whole document's edit list: the shared head, the changed middle and the
// shared tail. The head and tail are carried as equal edits (not as one line number) because the
// hunk's context lines may come from either side of the trimmed middle.
func buildEdits(oldLines, newLines []string, prefix, suffix int, oldMid, newMid []string) []edit {
	edits := make([]edit, 0, len(oldLines)+len(newLines))
	for i := range prefix {
		edits = append(edits, edit{kind: opEqual, oldNo: i + 1, newNo: i + 1, text: oldLines[i]})
	}
	oldNo, newNo := prefix+1, prefix+1
	for _, op := range diffOps(oldMid, newMid) {
		switch op.kind {
		case opEqual:
			edits = append(edits, edit{kind: opEqual, oldNo: oldNo, newNo: newNo, text: op.text})
			oldNo++
			newNo++
		case opDelete:
			edits = append(edits, edit{kind: opDelete, oldNo: oldNo, text: op.text})
			oldNo++
		case opInsert:
			edits = append(edits, edit{kind: opInsert, newNo: newNo, text: op.text})
			newNo++
		}
	}
	for i := range suffix {
		edits = append(edits, edit{
			kind:  opEqual,
			oldNo: len(oldLines) - suffix + i + 1,
			newNo: len(newLines) - suffix + i + 1,
			text:  oldLines[len(oldLines)-suffix+i],
		})
	}
	return edits
}

// diffOps computes an LCS edit script over two line slices. Above maxCells it gives up on the
// alignment and reports the whole middle as replaced: a readable "everything here changed" beats a
// diff that pins a small machine for seconds.
func diffOps(a, b []string) []edit {
	if len(a) == 0 {
		return insertAll(b)
	}
	if len(b) == 0 {
		return deleteAll(a)
	}
	if len(a)*len(b) > maxCells {
		return append(deleteAll(a), insertAll(b)...)
	}
	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	//
	// The table is filled BACKWARD, one suffix row at a time. The value names of the two ranges are
	// deliberately different: the `slices.Backward` rewrite of nested descending loops gives both
	// loops the same name, the inner one shadows the outer, and `v == v` silently compares a line
	// with itself — which made the whole diff match. (The outer `v` is then unused, so it does not
	// even compile.) Keep `ai` and `bj`.
	width := len(b) + 1
	lcs := make([]int32, (len(a)+1)*width)
	for i, ai := range slices.Backward(a) {
		for j, bj := range slices.Backward(b) {
			if ai == bj {
				lcs[i*width+j] = lcs[(i+1)*width+j+1] + 1
				continue
			}
			lcs[i*width+j] = max(lcs[(i+1)*width+j], lcs[i*width+j+1])
		}
	}
	out := make([]edit, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, edit{kind: opEqual, text: a[i]})
			i++
			j++
		case lcs[(i+1)*width+j] >= lcs[i*width+j+1]:
			out = append(out, edit{kind: opDelete, text: a[i]})
			i++
		default:
			out = append(out, edit{kind: opInsert, text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, edit{kind: opDelete, text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, edit{kind: opInsert, text: b[j]})
	}
	return out
}

func insertAll(lines []string) []edit {
	out := make([]edit, 0, len(lines))
	for _, line := range lines {
		out = append(out, edit{kind: opInsert, text: line})
	}
	return out
}

func deleteAll(lines []string) []edit {
	out := make([]edit, 0, len(lines))
	for _, line := range lines {
		out = append(out, edit{kind: opDelete, text: line})
	}
	return out
}

// span is an inclusive range of edit indices.
type span struct {
	start int
	end   int
}

// hunks expands every run of changes by the context lines around it, merging the runs whose windows
// touch. That is what makes two nearby edits one hunk instead of two overlapping ones.
func hunks(edits []edit) []span {
	var out []span
	for i := 0; i < len(edits); {
		if edits[i].kind == opEqual {
			i++
			continue
		}
		s := span{start: i, end: i}
		for i < len(edits) && edits[i].kind != opEqual {
			s.end = i
			i++
		}
		s.start = max(0, s.start-context)
		s.end = min(len(edits)-1, s.end+context)
		if len(out) > 0 && s.start <= out[len(out)-1].end+1 {
			out[len(out)-1].end = s.end
			continue
		}
		out = append(out, s)
	}
	return out
}

// render writes the unified text, stopping at the record's ceiling.
func render(edits []edit) (string, bool) {
	var b strings.Builder
	truncated := false
	lines := 0
	write := func(s string) bool {
		if lines >= maxOutputLines || b.Len()+len(s) > maxOutputBytes {
			truncated = true
			return false
		}
		b.WriteString(s)
		lines++
		return true
	}
	for _, h := range hunks(edits) {
		if !write(hunkHeader(edits, h)) {
			break
		}
		aborted := false
		for i := h.start; i <= h.end; i++ {
			if !write(editLine(edits[i])) {
				aborted = true
				break
			}
		}
		if aborted {
			break
		}
	}
	return b.String(), truncated
}

// hunkHeader writes the @@ line of one hunk: where it starts and how many lines of each body it
// covers.
func hunkHeader(edits []edit, h span) string {
	var oldStart, newStart int
	oldCount, newCount := 0, 0
	for i := h.start; i <= h.end; i++ {
		e := edits[i]
		if e.oldNo > 0 {
			if oldStart == 0 {
				oldStart = e.oldNo
			}
			oldCount++
		}
		if e.newNo > 0 {
			if newStart == 0 {
				newStart = e.newNo
			}
			newCount++
		}
	}
	if oldStart == 0 {
		oldStart = 1 // a pure insertion into an empty body
	}
	if newStart == 0 {
		newStart = 1
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
}

// editLine renders one edit the way a unified diff does.
func editLine(e edit) string {
	switch e.kind {
	case opDelete:
		return "-" + e.text + "\n"
	case opInsert:
		return "+" + e.text + "\n"
	default:
		return " " + e.text + "\n"
	}
}

// splitLines cuts a body into lines. CRLF is normalized (a snapshot is written on one machine and
// read on another), and a trailing newline does not produce an empty last line.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// commonPrefix counts the lines both bodies start with.
func commonPrefix(a, b []string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// commonSuffix counts the lines both bodies end with, looking at the tails only.
func commonSuffix(a, b []string) int {
	n := min(len(a), len(b))
	for i := 1; i <= n; i++ {
		if a[len(a)-i] != b[len(b)-i] {
			return i - 1
		}
	}
	return n
}
