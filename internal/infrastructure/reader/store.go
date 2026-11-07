// Package reader persists the reader's shared "new" watermark in the vault: one record, not one per
// client. A source is NEW when its arrival is later than SeenAt.
//
// The file is app state, hidden with the rest of .thought-keeper/ and git-ignored: an acknowledgment
// rewrites it whenever something new arrived, and versioning that churn would bury the knowledge. It
// is NOT derived — reindex and rebuild-trees leave it alone, because no source can tell when the
// reader last looked.
package reader

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// File is the record's name inside the reader-state directory.
const File = "seen.md"

// Store reads and writes the seen watermark of one vault.
type Store struct {
	root string
	mu   sync.Mutex
}

// New returns a store rooted at the vault's reader-state directory.
func New(root string) *Store { return &Store{root: root} }

// Path is the watermark record's absolute path.
func (s *Store) Path() string { return filepath.Join(s.root, File) }

type frontmatter struct {
	SeenAt string `yaml:"seen_at"`
}

// Read returns the watermark and whether the record exists. A malformed record is treated as
// absent: one damaged line of app state must not hide the vault, and the next activation rewrites
// it.
func (s *Store) Read(_ context.Context) (time.Time, bool, error) {
	b, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("reader: read seen watermark: %w", err)
	}
	text := strings.TrimPrefix(string(b), "---\n")
	before, _, ok := strings.Cut(text, "\n---")
	if !ok {
		return time.Time{}, false, nil
	}
	var fm frontmatter
	decodeErr := yaml.Unmarshal([]byte(before), &fm)
	at, parseErr := time.Parse(time.RFC3339Nano, fm.SeenAt)
	valid := decodeErr == nil && parseErr == nil && !at.IsZero()
	if !valid {
		// A damaged record is a vault no client has acknowledged yet, not an error: the next
		// activation rewrites it.
		return time.Time{}, false, nil
	}
	return at.UTC(), true, nil
}

// Write stores the watermark atomically (temp file → rename).
func (s *Store) Write(_ context.Context, seenAt time.Time) error {
	if seenAt.IsZero() {
		return fmt.Errorf("reader: a seen watermark needs an instant")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("reader: mkdir: %w", err)
	}
	b := encode(seenAt)
	tmp, err := os.CreateTemp(s.root, ".seen-*.tmp")
	if err != nil {
		return fmt.Errorf("reader: temp: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() //nolint:errcheck // a trash move is best effort; a missing file is the goal
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close() //nolint:errcheck // a close or an unlock has nothing left to report
		return fmt.Errorf("reader: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("reader: close: %w", err)
	}
	if err := os.Rename(name, s.Path()); err != nil {
		return fmt.Errorf("reader: rename: %w", err)
	}
	return nil
}

func encode(seenAt time.Time) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("seen_at: " + seenAt.UTC().Format(time.RFC3339Nano) + "\n")
	b.WriteString("---\n")
	return []byte(b.String())
}
