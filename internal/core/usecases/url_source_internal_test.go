package usecases

import (
	"strings"
	"testing"
)

// TestSnapshotBodyIgnoresTheFrontmatter: the refresh compares the BODY of the stored snapshot with the
// body it just read. The frontmatter carries the title and the fetch time; neither is content.
func TestSnapshotBodyIgnoresTheFrontmatter(t *testing.T) {
	snapshot := "---\nurl: https://example.com/x\nfetched_at: 2026-01-01T00:00:00Z\ntitle: T\nrendered: lightpanda\n---\n\n# Body\n"
	if got := strings.TrimSpace(string(snapshotBody([]byte(snapshot)))); got != "# Body" {
		t.Errorf("body = %q, want the text after the frontmatter", got)
	}
	raw := []byte("# No frontmatter\n")
	if got := string(snapshotBody(raw)); got != string(raw) {
		t.Errorf("a plain body was changed: %q", got)
	}
}
