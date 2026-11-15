// Package reader persists the reader's two shared seen watermarks in the vault: one record, not one
// per client. Start's watermark drives the statistic the front door shows; Sources' watermark
// drives the new and updated marks of its rows.
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

// Store reads and writes the seen watermarks of one vault.
type Store struct {
	root string
	mu   sync.Mutex
}

// New returns a store rooted at the vault's reader-state directory.
func New(root string) *Store { return &Store{root: root} }

// Path is the watermark record's absolute path.
func (s *Store) Path() string { return filepath.Join(s.root, File) }

type frontmatter struct {
	// SeenAt is the single watermark of older records. It seeds both scopes on first read and is
	// never written again.
	SeenAt        string `yaml:"seen_at"`
	StartSeenAt   string `yaml:"start_seen_at"`
	SourcesSeenAt string `yaml:"sources_seen_at"`
}

// Read returns both watermarks. A malformed field is treated as absent: one damaged line of app
// state must not hide the vault, and the next activation rewrites it.
func (s *Store) Read(_ context.Context) (entities.ReaderState, error) {
	b, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entities.ReaderState{}, nil
		}
		return entities.ReaderState{}, fmt.Errorf("reader: read seen watermark: %w", err)
	}
	text := strings.TrimPrefix(string(b), "---\n")
	before, _, ok := strings.Cut(text, "\n---")
	if !ok {
		return entities.ReaderState{}, nil
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(before), &fm); err != nil {
		//nolint:nilerr // a damaged record is a vault no client has acknowledged yet, not a failure
		return entities.ReaderState{}, nil
	}
	state := entities.ReaderState{
		StartSeenAt:   parseInstant(fm.StartSeenAt),
		SourcesSeenAt: parseInstant(fm.SourcesSeenAt),
	}
	// A record written before the two watermarks existed carries one instant: it was acknowledged
	// by Start or Sources alike, so it seeds both. A scope that already has its own value keeps it.
	if legacy := parseInstant(fm.SeenAt); !legacy.IsZero() {
		if state.StartSeenAt.IsZero() {
			state.StartSeenAt = legacy
		}
		if state.SourcesSeenAt.IsZero() {
			state.SourcesSeenAt = legacy
		}
	}
	return state, nil
}

// Write stores both watermarks atomically (temp file → rename). A zero scope is omitted: the first
// visit to the other lens initializes it, and a Start visit must never speak for Sources.
func (s *Store) Write(_ context.Context, state entities.ReaderState) error {
	if state.StartSeenAt.IsZero() && state.SourcesSeenAt.IsZero() {
		return fmt.Errorf("reader: a seen watermark needs an instant")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("reader: mkdir: %w", err)
	}
	b := encode(state)
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

func encode(state entities.ReaderState) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	if !state.StartSeenAt.IsZero() {
		b.WriteString("start_seen_at: " + state.StartSeenAt.UTC().Format(time.RFC3339Nano) + "\n")
	}
	if !state.SourcesSeenAt.IsZero() {
		b.WriteString("sources_seen_at: " + state.SourcesSeenAt.UTC().Format(time.RFC3339Nano) + "\n")
	}
	b.WriteString("---\n")
	return []byte(b.String())
}
