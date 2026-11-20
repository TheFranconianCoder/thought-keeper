// Package reader persists the reader's seen watermark in the vault: one record, not one per client.
// The instant is the batch of sources the reader has not read yet — the arrivals and the page
// content changes since the Sources list was last opened. The Start lens summarizes the same batch.
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

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
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
	// SourcesSeenAt is the vault's seen watermark: the instant opening the list acknowledged. Both
	// lenses read it.
	SourcesSeenAt string `yaml:"sources_seen_at"`
	// SeenAt is the single watermark of a record that predates the split. It seeds the value on read
	// and is dropped by the next write, as is the retired start_seen_at line (an unknown key, so it
	// never reaches the state).
	SeenAt string `yaml:"seen_at"`
}

// Read returns the watermark. A malformed field is treated as absent: one damaged line of app state
// must not hide the vault, and the next acknowledgment rewrites it.
func (s *Store) Read(_ context.Context) (entities.ReaderState, error) {
	fm, err := s.readRecord()
	if err != nil {
		return entities.ReaderState{}, err
	}
	if at := parseInstant(fm.SourcesSeenAt); !at.IsZero() {
		return entities.ReaderState{SourcesSeenAt: at}, nil
	}
	// A record written before the two watermarks existed carries one instant; it was acknowledged by
	// both lenses alike, and the batch's baseline has not moved since.
	return entities.ReaderState{SourcesSeenAt: parseInstant(fm.SeenAt)}, nil
}

// readRecord returns the record as STORED.
func (s *Store) readRecord() (frontmatter, error) {
	var fm frontmatter
	b, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fm, nil
		}
		return fm, fmt.Errorf("reader: read seen watermark: %w", err)
	}
	text := strings.TrimPrefix(string(b), "---\n")
	before, _, ok := strings.Cut(text, "\n---")
	if !ok {
		return fm, nil
	}
	if err := yaml.Unmarshal([]byte(before), &fm); err != nil {
		//nolint:nilerr // a damaged record is a vault no client has acknowledged yet, not a failure
		return frontmatter{}, nil
	}
	return fm, nil
}

// WriteSeen stores the watermark one acknowledgment settled, atomically (temp file → rename), and
// drops the keys older records carried.
func (s *Store) WriteSeen(_ context.Context, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("reader: a seen watermark needs an instant")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fm, err := s.readRecord()
	if err != nil {
		return err
	}
	fm.SourcesSeenAt = instant(at)
	fm.SeenAt = ""
	return s.write(fm)
}

func (s *Store) write(fm frontmatter) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("reader: mkdir: %w", err)
	}
	b := encode(fm)
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

func parseInstant(raw string) time.Time {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || at.IsZero() {
		return time.Time{}
	}
	return at.UTC()
}

func instant(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }

func encode(fm frontmatter) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	if fm.SeenAt != "" {
		b.WriteString("seen_at: " + fm.SeenAt + "\n")
	}
	if fm.SourcesSeenAt != "" {
		b.WriteString("sources_seen_at: " + fm.SourcesSeenAt + "\n")
	}
	b.WriteString("---\n")
	return []byte(b.String())
}
