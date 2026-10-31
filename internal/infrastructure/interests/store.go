// Package interests persists the reader's standing interest rules in the vault:
// <vault>/interests/<slug>.md, one Markdown file per rule.
//
// The file is the truth, like the vocabulary and the wiki pages. A rule is a DECISION (which words
// mean "this is mine", what to leave out, whether it is paused) and no index can rebuild it. Its
// matches are not stored here: the queue entry names the slug, and the evidence is computed on
// demand from the claims and the labels the vault already has.
package interests

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// Ext is the file extension of one interest (<slug>.md).
const Ext = ".md"

// Store reads and writes the interests of one vault.
type Store struct {
	root string
	mu   sync.Mutex
	// locks serialises the read-modify-write of ONE rule (a rejection from two windows).
	locks map[string]*sync.Mutex
}

// New returns a store rooted at the vault's interests/ directory.
func New(root string) *Store { return &Store{root: root} }

// Path is a rule's file.
func (s *Store) Path(slug string) string {
	return filepath.Join(s.root, slug+Ext)
}

// List returns every rule, oldest first. A missing directory is a vault that has never created one,
// not an error.
func (s *Store) List(_ context.Context) ([]entities.Interest, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]entities.Interest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), Ext) || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), Ext)
		rule, err := s.read(slug)
		if err != nil {
			continue // a damaged rule must not hide the others
		}
		out = append(out, rule)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// Get reads one rule by slug.
func (s *Store) Get(_ context.Context, slug string) (entities.Interest, error) {
	return s.read(slug)
}

// Save writes one rule atomically (temp file → rename).
func (s *Store) Save(_ context.Context, rule entities.Interest) error {
	if rule.Slug == "" {
		return fmt.Errorf("interests: slug is required")
	}
	unlock := s.lock(rule.Slug)
	defer unlock()
	return s.write(rule)
}

// Delete removes one rule. A missing file is not an error: the rule is gone either way.
func (s *Store) Delete(_ context.Context, slug string) error {
	unlock := s.lock(slug)
	defer unlock()
	if err := os.Remove(s.Path(slug)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// SetRejected records (or clears) one source's correction. It is a read-modify-write under the
// rule's lock, so two windows cannot lose each other's rejection.
func (s *Store) SetRejected(_ context.Context, slug, hash string, rejected bool) (entities.Interest, error) {
	unlock := s.lock(slug)
	defer unlock()
	rule, err := s.read(slug)
	if err != nil {
		return entities.Interest{}, err
	}
	set := make([]string, 0, len(rule.Rejected)+1)
	for _, h := range rule.Rejected {
		if h != hash {
			set = append(set, h)
		}
	}
	if rejected {
		set = append(set, hash)
	}
	rule.Rejected = set
	rule.Updated = time.Now().UTC()
	if err := s.write(rule); err != nil {
		return entities.Interest{}, err
	}
	return rule, nil
}

func (s *Store) read(slug string) (entities.Interest, error) {
	b, err := os.ReadFile(s.Path(slug))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entities.Interest{}, fmt.Errorf("interests: %s: %w", slug, fs.ErrNotExist)
		}
		return entities.Interest{}, err
	}
	return decode(slug, b)
}

func (s *Store) write(rule entities.Interest) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	b, err := encode(rule)
	if err != nil {
		return err
	}
	dst := s.Path(rule.Slug)
	tmp, err := os.CreateTemp(s.root, "."+rule.Slug+"-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}

// lock serialises the read-modify-write of one rule.
func (s *Store) lock(slug string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[string]*sync.Mutex)
	}
	l, ok := s.locks[slug]
	if !ok {
		l = &sync.Mutex{}
		s.locks[slug] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

type frontmatter struct {
	Kind     string   `yaml:"kind"`
	Slug     string   `yaml:"slug"`
	Name     string   `yaml:"name"`
	Terms    []string `yaml:"terms,omitempty"`
	Exclude  []string `yaml:"exclude,omitempty"`
	Kinds    []string `yaml:"kinds,omitempty"`
	Enabled  *bool    `yaml:"enabled,omitempty"`
	Rejected []string `yaml:"rejected,omitempty"`
	Created  string   `yaml:"created,omitempty"`
	Updated  string   `yaml:"updated,omitempty"`
}

func encode(rule entities.Interest) ([]byte, error) {
	enabled := rule.Enabled
	fm := frontmatter{
		Kind: "interest", Slug: rule.Slug, Name: rule.Name,
		Terms: rule.Terms, Exclude: rule.Exclude, Kinds: rule.Kinds,
		Enabled: &enabled, Rejected: rule.Rejected,
	}
	if !rule.Created.IsZero() {
		fm.Created = rule.Created.UTC().Format(time.RFC3339)
	}
	if !rule.Updated.IsZero() {
		fm.Updated = rule.Updated.UTC().Format(time.RFC3339)
	}
	var b strings.Builder
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	b.WriteString("---\n")
	return []byte(b.String()), nil
}

func decode(slug string, data []byte) (entities.Interest, error) {
	raw := string(data)
	text := strings.TrimPrefix(raw, "---\n")
	before, _, ok := strings.Cut(text, "\n---")
	if !ok {
		return entities.Interest{}, fmt.Errorf("interests: %s: unterminated frontmatter", slug)
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(before), &fm); err != nil {
		return entities.Interest{}, fmt.Errorf("interests: %s: %w", slug, err)
	}
	if fm.Slug == "" {
		fm.Slug = slug
	}
	rule := entities.Interest{
		Slug: fm.Slug, Name: strings.TrimSpace(fm.Name),
		Terms: fm.Terms, Exclude: fm.Exclude, Kinds: fm.Kinds,
		Enabled: true, Rejected: fm.Rejected,
	}
	if fm.Enabled != nil {
		rule.Enabled = *fm.Enabled
	}
	if rule.Name == "" {
		rule.Name = rule.Slug
	}
	if t, err := time.Parse(time.RFC3339, fm.Created); err == nil {
		rule.Created = t
	}
	if t, err := time.Parse(time.RFC3339, fm.Updated); err == nil {
		rule.Updated = t
	}
	return rule, nil
}
