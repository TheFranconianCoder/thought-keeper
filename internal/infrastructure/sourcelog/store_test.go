package sourcelog

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

const hash = "3dd5f7fe15e29619cd4072654cb9c5ec940ff527a864d758715591b59d5800db"

// TestAppendAndRead: one greppable line per event, in order, readable back.
func TestAppendAndRead(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	at := time.Date(2026, 10, 17, 2, 22, 31, 0, time.UTC)

	s.Append(ctx, hash, interfaces.SourceLogEvent{
		At: at, RunID: "01M53", Phase: "tree", Message: "extracted and tree built",
		Fields: map[string]any{"nodes": 713, "pages": 512, "ocr": false},
	})
	s.Append(ctx, hash, interfaces.SourceLogEvent{
		At: at.Add(time.Minute), RunID: "01M53", Phase: "distill", Level: "warn",
		Message: "part hit the iteration cap, will be redone",
		Fields:  map[string]any{"part": "4", "atoms": 3},
	})

	got, err := s.Read(ctx, hash)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2:\n%s", len(lines), got)
	}
	first := lines[0]
	for _, want := range []string{
		"2026-10-17T02:22:31Z", "run=01M53", "level=info", "phase=tree",
		`msg="extracted and tree built"`, "nodes=713", "pages=512", "ocr=false",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first line misses %q:\n%s", want, first)
		}
	}
	if !strings.Contains(lines[1], "level=warn") || !strings.Contains(lines[1], "part=4") {
		t.Errorf("second line = %q", lines[1])
	}
}

// TestInvalidHashIsIgnored: a log write must never break the pipeline it observes.
func TestInvalidHashIsIgnored(t *testing.T) {
	s := New(t.TempDir())
	s.Append(context.Background(), "not-a-hash", interfaces.SourceLogEvent{Message: "x"})
	if got, err := s.Read(context.Background(), hash); err != nil || got != "" {
		t.Errorf("unexpected content for a valid hash: %q %v", got, err)
	}
}

// TestRotation: a book's log is capped, the previous part kept as .1.
func TestRotation(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	line := interfaces.SourceLogEvent{At: time.Now().UTC(), Phase: "distill", Message: strings.Repeat("x", 200)}
	for range 6000 { // ~1.2 MiB, over the cap
		s.Append(ctx, hash, line)
	}
	rotated, err := readIfExists(t, s.path(hash)+".1")
	if err != nil {
		t.Fatalf("rotated: %v", err)
	}
	if rotated == "" {
		t.Fatal("the log did not rotate")
	}
	if cur, _ := readIfExists(t, s.path(hash)); len(cur) > maxBytes+4096 {
		t.Errorf("current log is %d bytes, cap is %d", len(cur), maxBytes)
	}
	// Read returns both parts, oldest first.
	all, err := s.Read(ctx, hash)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) < len(rotated) {
		t.Errorf("read = %d bytes, want the rotation plus the current part", len(all))
	}
}

func readIfExists(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestRemoveDropsTheLogAndItsRotation: a reset must leave nothing of the old derivation behind, and
// the log rotates — both files go.
func TestRemoveDropsTheLogAndItsRotation(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	hash := strings.Repeat("ab", 32)
	ctx := context.Background()
	st.Append(ctx, hash, interfaces.SourceLogEvent{At: time.Now(), Phase: "distill", Message: "old story"})

	// The log lives NEXT TO THE SOURCE: sources/<ab>/<cd>/<hash>.log — same sharding as the object.
	logPath := objects.SidecarPath(dir, hash, entities.SidecarLog)
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("the log was not written: %v", err)
	}
	if err := st.Remove(ctx, hash); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, p := range []string{logPath, logPath + ".1"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists (err=%v)", p, err)
		}
	}
	text, err := st.Read(ctx, hash)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if text != "" {
		t.Errorf("read = %q, want empty after a remove", text)
	}
	// Removing again is not an error: the caller wants it gone.
	if err := st.Remove(ctx, hash); err != nil {
		t.Errorf("second remove: %v", err)
	}
}
