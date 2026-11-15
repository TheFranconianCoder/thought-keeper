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
	// SeenAt is the single watermark of older records. It seeds both scopes on read and survives a
	// write until both scopes carry an instant of their own.
	SeenAt        string `yaml:"seen_at"`
	StartSeenAt   string `yaml:"start_seen_at"`
	SourcesSeenAt string `yaml:"sources_seen_at"`
}

// Read returns both watermarks. A malformed field is treated as absent: one damaged line of app
// state must not hide the vault, and the next activation rewrites it.
func (s *Store) Read(_ context.Context) (entities.ReaderState, error) {
	fm, err := s.readRecord()
	if err != nil {
		return entities.ReaderState{}, err
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

// readRecord returns the record as STORED, without the legacy seeding: a write must never turn one
// lens's inherited baseline into an acknowledgment of that lens.
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

// WriteScopes stores the watermarks ONE visit acknowledged, atomically (temp file → rename), and
// leaves every scope it is not given exactly as stored: opening Start must never write
// sources_seen_at, and the record never materializes a scope the reader did not visit.
//
// The legacy instant survives while a scope is still unacknowledged, because it is that scope's
// baseline on the next read; once both scopes have an instant of their own the line is dropped.
func (s *Store) WriteScopes(_ context.Context, marks []entities.ReaderWatermark) error {
	if len(marks) == 0 {
		return fmt.Errorf("reader: an acknowledgment needs a watermark")
	}
	for _, mark := range marks {
		if !mark.Scope.Valid() {
			return fmt.Errorf("reader: unknown scope %q", mark.Scope)
		}
		if mark.SeenAt.IsZero() {
			return fmt.Errorf("reader: a seen watermark needs an instant")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fm, err := s.readRecord()
	if err != nil {
		return err
	}
	for _, mark := range marks {
		if mark.Scope == entities.ReaderScopeSources {
			fm.SourcesSeenAt = instant(mark.SeenAt)
		} else {
			fm.StartSeenAt = instant(mark.SeenAt)
		}
	}
	if fm.StartSeenAt != "" && fm.SourcesSeenAt != "" {
		fm.SeenAt = ""
	}
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
	if fm.StartSeenAt != "" {
		b.WriteString("start_seen_at: " + fm.StartSeenAt + "\n")
	}
	if fm.SourcesSeenAt != "" {
		b.WriteString("sources_seen_at: " + fm.SourcesSeenAt + "\n")
	}
	b.WriteString("---\n")
	return []byte(b.String())
}
