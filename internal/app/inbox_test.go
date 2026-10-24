package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
)

// TestPolledInboxStillTakesADroppedFile: when the kernel refuses an inotify watch (ENOSPC means the
// per-user watch budget, never the disk), the inbox is POLLED — and a file dropped into it must still
// become a source. The drop zone is a promise; the watch is only how it is kept.
func TestPolledInboxStillTakesADroppedFile(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := sqlc.NewSourceRepository(db)

	a := &App{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg: config.Config{Pipeline: config.PipelineConfig{Debounce: "10ms"}},
		intake: usecases.IntakeSource{
			Store: objects.New(filepath.Join(root, "sources")), Index: index, Clock: systemClock{},
		},
		dropsInflight: map[string]bool{},
		queued:        map[string]bool{},
		claimedAt:     map[string]time.Time{},
		distillActive: map[string]bool{},
	}

	inbox := filepath.Join(root, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("inbox: %v", err)
	}
	path := filepath.Join(inbox, "note.md")
	if err := os.WriteFile(path, []byte("# Caching\n\nCaching is only safe when the key covers every input.\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A file that is still being copied is not picked up: the scan wants a settled mtime.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Twice, as two scans would run: one intake per file, not two sources.
	a.scanInbox(inbox)
	a.scanInbox(inbox)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the polled inbox never took the dropped file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	sources, err := index.ListSources(context.Background(), "", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %d, want exactly the dropped file", len(sources))
	}
	if sources[0].Kind != "md" || sources[0].Title != "Caching" {
		t.Fatalf("source = %+v, want the Markdown file with its own title", sources[0])
	}
}

// TestInboxWatchHintExplainsENOSPC: "no space left on device" from inotify is not a full disk, and the
// log has to say so — that sentence sent the reader to check their disk.
func TestInboxWatchHintExplainsENOSPC(t *testing.T) {
	if got := inboxWatchHint(os.ErrDeadlineExceeded); got != "" {
		t.Errorf("hint for an unrelated error = %q, want none", got)
	}
	hint := inboxWatchHint(syscall.ENOSPC)
	if hint == "" || !strings.Contains(hint, "NOT the disk") {
		t.Fatalf("hint = %q, want the inotify explanation", hint)
	}
}
