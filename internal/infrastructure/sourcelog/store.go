// Package sourcelog writes the per-source log: one append-only text file per source, NEXT TO THE
// SOURCE ITSELF (sources/<ab>/<cd>/<hash>.log) — greppable in the vault, and gone with the source
// when it is deleted, because it is part of that source's story.
package sourcelog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// maxBytes caps one source's log; when it is exceeded the file is rotated to ".1" so a
// 500-page book's derivation cannot grow the log without bound.
const maxBytes = 1 << 20

// Store keeps one log file per source, next to the source object.
type Store struct {
	root string
	mu   sync.Mutex
	// OnAppend is called with every written event (and its rendered line), so a live view can
	// show it as it happens and derive progress from it (the PWA subscribes over SSE — no polling).
	OnAppend func(hash string, e interfaces.SourceLogEvent, line string)
}

// New returns a store over a sources/ directory (shard directories are created on first write).
func New(sourcesDir string) *Store { return &Store{root: sourcesDir} }

var _ interfaces.SourceLog = (*Store)(nil)

// Append writes one rendered line. It is best-effort by design: a log must never break the
// pipeline that it observes.
func (s *Store) Append(_ context.Context, hash string, e interfaces.SourceLogEvent) {
	if !entities.IsSHA256Hex(hash) {
		return
	}
	line := render(e)
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
	if s.OnAppend != nil {
		// Out of the lock (a subscriber may do work), but guaranteed to be taken again: the
		// deferred Unlock runs last.
		s.mu.Unlock()
		defer s.mu.Lock()
		s.OnAppend(hash, e, line)
	}
}

// Read returns the whole log (newest rotation first) so an API can show it.
func (s *Store) Read(_ context.Context, hash string) (string, error) {
	if !entities.IsSHA256Hex(hash) {
		return "", fmt.Errorf("invalid hash")
	}
	var b strings.Builder
	if prev, err := os.ReadFile(s.path(hash) + ".1"); err == nil {
		b.Write(prev)
	}
	cur, err := os.ReadFile(s.path(hash))
	if err != nil {
		if os.IsNotExist(err) {
			return b.String(), nil
		}
		return "", err
	}
	b.Write(cur)
	return b.String(), nil
}

// Remove deletes a source's log and its rotated older half. Missing files are not an error: the
// caller wants them gone.
func (s *Store) Remove(_ context.Context, hash string) error {
	if !entities.IsSHA256Hex(hash) {
		return fmt.Errorf("invalid hash")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range []string{s.path(hash), s.path(hash) + ".1"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// path is a source's log file: <sources>/<ab>/<cd>/<hash>.log, next to the source itself.
func (s *Store) path(hash string) string {
	return objects.SidecarPath(s.root, hash, entities.SidecarLog)
}

// render formats one event as a single greppable line:
//
//	2026-10-17T02:22:31Z run=01M53… level=info phase=distill msg="part done" atoms=17
func render(e interfaces.SourceLogEvent) string {
	var b strings.Builder
	at := e.At.UTC().Format("2006-01-02T15:04:05Z")
	level := e.Level
	if level == "" {
		level = "info"
	}
	b.WriteString(at)
	if e.RunID != "" {
		fmt.Fprintf(&b, " run=%s", e.RunID)
	}
	fmt.Fprintf(&b, " level=%s", level)
	if e.Phase != "" {
		fmt.Fprintf(&b, " phase=%s", e.Phase)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, " msg=%s", quote(e.Message))
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if txt, ok := e.Fields[k].(string); ok && txt == "" {
			continue // an empty field says nothing; keep the line readable
		}
		fmt.Fprintf(&b, " %s=%s", k, value(e.Fields[k]))
	}
	b.WriteString("\n")
	return b.String()
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func value(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case string:
		return quote(t)
	case error:
		return quote(t.Error())
	default:
		return fmt.Sprint(v)
	}
}
