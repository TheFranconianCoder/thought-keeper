package textdiff_test

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/textdiff"
)

func body(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestIdenticalBodiesProduceNothing(t *testing.T) {
	text := body("one", "two", "three")
	res := textdiff.Lines(text, text)
	if res.Text != "" || res.Added != 0 || res.Removed != 0 || res.Truncated {
		t.Fatalf("identical bodies = %+v, want an empty result", res)
	}
}

func TestOneChangedLineIsOneHunk(t *testing.T) {
	oldText := body("a", "b", "c", "d", "e", "f", "g", "h")
	newText := body("a", "B", "c", "d", "e", "f", "g", "h")
	res := textdiff.Lines(oldText, newText)
	want := "@@ -1,5 +1,5 @@\n a\n-b\n+B\n c\n d\n e\n"
	if res.Text != want {
		t.Fatalf("diff =\n%q\nwant\n%q", res.Text, want)
	}
	if res.Added != 1 || res.Removed != 1 {
		t.Fatalf("counts = +%d -%d, want +1 -1", res.Added, res.Removed)
	}
}

func TestAReflowIsADiff(t *testing.T) {
	// The words are the same; the wrapping is not. The record reports what the page says now, so
	// this is a change — the claim tolerance is the rebuild's business, not the record's.
	res := textdiff.Lines(body("one two", "three four"), body("one", "two three four"))
	if res.Text == "" || res.Added == 0 || res.Removed == 0 {
		t.Fatalf("reflow = %+v, want a diff", res)
	}
}

func TestInsertionAndDeletion(t *testing.T) {
	ins := textdiff.Lines(body("a", "c"), body("a", "b", "c"))
	if ins.Added != 1 || ins.Removed != 0 || !strings.Contains(ins.Text, "+b") {
		t.Fatalf("insertion = %+v", ins)
	}
	del := textdiff.Lines(body("a", "b", "c"), body("a", "c"))
	if del.Added != 0 || del.Removed != 1 || !strings.Contains(del.Text, "-b") {
		t.Fatalf("deletion = %+v", del)
	}
}

func TestEmptyBodies(t *testing.T) {
	fromNothing := textdiff.Lines("", body("a", "b"))
	if fromNothing.Added != 2 || fromNothing.Removed != 0 {
		t.Fatalf("from nothing = %+v", fromNothing)
	}
	if !strings.HasPrefix(fromNothing.Text, "@@ -1,0 +1,2 @@\n") {
		t.Fatalf("from-nothing header = %q", fromNothing.Text)
	}
	toNothing := textdiff.Lines(body("a", "b"), "")
	if toNothing.Removed != 2 || !strings.HasPrefix(toNothing.Text, "@@ -1,2 +1,0 @@\n") {
		t.Fatalf("to nothing = %+v", toNothing)
	}
	if empty := textdiff.Lines("", ""); empty.Text != "" {
		t.Fatalf("both empty = %+v", empty)
	}
}

func TestNearbyChangesShareAHunk(t *testing.T) {
	oldText := body("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	newText := body("a", "B", "c", "d", "E", "f", "g", "h", "i", "j")
	res := textdiff.Lines(oldText, newText)
	if got := strings.Count(res.Text, "@@ "); got != 1 {
		t.Fatalf("hunks = %d, want 1 merged hunk:\n%s", got, res.Text)
	}
	if res.Added != 2 || res.Removed != 2 {
		t.Fatalf("counts = +%d -%d, want +2 -2", res.Added, res.Removed)
	}
}

func TestFarApartChangesAreSeparateHunks(t *testing.T) {
	oldLines := make([]string, 40)
	newLines := make([]string, 40)
	for i := range oldLines {
		oldLines[i] = "line"
		newLines[i] = "line"
	}
	oldLines[0] = "first"
	newLines[0] = "FIRST"
	oldLines[39] = "last"
	newLines[39] = "LAST"
	res := textdiff.Lines(body(oldLines...), body(newLines...))
	if got := strings.Count(res.Text, "@@ "); got != 2 {
		t.Fatalf("hunks = %d, want 2:\n%s", got, res.Text)
	}
}

func TestEveryBodyLineIsPrefixed(t *testing.T) {
	res := textdiff.Lines(body("a", "b", "c"), body("a", "x", "c", "d"))
	for line := range strings.SplitSeq(strings.TrimSuffix(res.Text, "\n"), "\n") {
		if strings.HasPrefix(line, "@@ ") {
			continue
		}
		if line == "" || (line[0] != ' ' && line[0] != '-' && line[0] != '+') {
			t.Fatalf("line %q has no diff prefix", line)
		}
	}
}

func TestHugeChangeIsTruncated(t *testing.T) {
	oldLines := make([]string, 6000)
	newLines := make([]string, 6000)
	for i := range oldLines {
		oldLines[i] = "old " + strings.Repeat("x", 40)
		newLines[i] = "new " + strings.Repeat("y", 40)
	}
	res := textdiff.Lines(body(oldLines...), body(newLines...))
	if !res.Truncated {
		t.Fatalf("a 6000-line rewrite was not truncated (text %d bytes)", len(res.Text))
	}
	if len(res.Text) > 256<<10 {
		t.Fatalf("record grew to %d bytes, ceiling is 256 KiB", len(res.Text))
	}
	if res.Added != 6000 || res.Removed != 6000 {
		t.Fatalf("counts = +%d -%d, want the whole change +6000 -6000", res.Added, res.Removed)
	}
}

func TestCRLFIsNormalized(t *testing.T) {
	res := textdiff.Lines("a\r\nb\r\n", "a\nb\n")
	if res.Text != "" {
		t.Fatalf("CRLF-only difference = %+v, want none", res)
	}
}
