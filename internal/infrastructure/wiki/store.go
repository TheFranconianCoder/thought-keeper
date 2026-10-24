// Package wiki stores user-authored wiki pages as Markdown files with YAML frontmatter.
//
// The page is the truth: it carries its ID, title, tags and the address of the source
// representation. The source representation is derived from the page, exactly as a URL source is a
// snapshot of a page that changes; an edit rewrites that representation in place and re-runs the
// pipeline from capture.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"gopkg.in/yaml.v3"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/idpath"
)

// Store is the wiki page store rooted at the vault's wiki/ directory.
type Store struct{ root string }

// New returns a store rooted at root.
func New(root string) *Store { return &Store{root: root} }

// Shard is the two-level folder a page lives in, derived from the tail of its ULID.
func Shard(id ulid.ULID) string { return idpath.Shard(id) }

// Path is a page's vault-relative path: <ab>/<cd>/<id>.md.
func Path(id ulid.ULID) string {
	return filepath.ToSlash(filepath.Join(Shard(id), id.String()+".md"))
}

type frontmatter struct {
	ID    string   `yaml:"id"`
	Title string   `yaml:"title"`
	Tags  []string `yaml:"tags,omitempty"`
	// AutoTags are derived by the app (the tagging run), never by a human. They are a separate key so a
	// save keeps them apart from what the reader wrote — and so deleting a derived tag sticks.
	AutoTags   []string `yaml:"auto_tags,omitempty"`
	URLs       []string `yaml:"urls,omitempty"`
	SourceHash string   `yaml:"source,omitempty"`
	Created    string   `yaml:"created,omitempty"`
	Updated    string   `yaml:"updated,omitempty"`
}

// Encode renders a page as a Markdown file (frontmatter + body). It is exported because the page
// file is also the source representation's human-readable shape.
func Encode(page entities.WikiPage) ([]byte, error) {
	fm := frontmatter{
		ID:         page.ID.String(),
		Title:      strings.TrimSpace(page.Title),
		Tags:       NormalizeTags(page.Tags),
		AutoTags:   NormalizeTags(page.AutoTags),
		URLs:       append([]string(nil), page.URLs...),
		SourceHash: page.SourceHash,
	}
	if !page.CreatedAt.IsZero() {
		fm.Created = page.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !page.UpdatedAt.IsZero() {
		fm.Updated = page.UpdatedAt.UTC().Format(time.RFC3339)
	}
	var b strings.Builder
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("wiki: encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("wiki: close frontmatter: %w", err)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(page.Body, "\n"))
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// Decode parses a page file.
func Decode(data []byte) (entities.WikiPage, error) {
	raw := string(data)
	if !strings.HasPrefix(raw, "---\n") {
		return entities.WikiPage{}, fmt.Errorf("wiki: page has no frontmatter")
	}
	rest := raw[len("---\n"):]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return entities.WikiPage{}, fmt.Errorf("wiki: unterminated frontmatter")
	}
	fmText := before
	body := strings.TrimLeft(after, "\n")
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return entities.WikiPage{}, fmt.Errorf("wiki: decode frontmatter: %w", err)
	}
	id, err := ulid.ParseStrict(fm.ID)
	if err != nil {
		return entities.WikiPage{}, fmt.Errorf("wiki: invalid page id %q: %w", fm.ID, err)
	}
	page := entities.WikiPage{
		ID: id, Title: strings.TrimSpace(fm.Title), Tags: NormalizeTags(fm.Tags),
		AutoTags: NormalizeTags(fm.AutoTags),
		URLs:     append([]string(nil), fm.URLs...), Body: strings.TrimRight(body, "\n"), SourceHash: fm.SourceHash,
	}
	if t, err := time.Parse(time.RFC3339, fm.Created); err == nil {
		page.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, fm.Updated); err == nil {
		page.UpdatedAt = t
	}
	return page, nil
}

// Write stores a page atomically at its canonical path, through a UNIQUE temp file: a page is written
// by the reader's save, by the tagging run and by a vocabulary rename, and one shared "<page>.tmp" let
// two of them publish each other's half-written bytes.
func (s *Store) Write(_ context.Context, page entities.WikiPage) error {
	page.Tags = NormalizeTags(page.Tags)
	page.AutoTags = NormalizeTags(page.AutoTags)
	b, err := Encode(page)
	if err != nil {
		return err
	}
	dst := filepath.Join(s.root, filepath.FromSlash(Path(page.ID)))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("wiki: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+page.ID.String()[:8]+"-*.tmp")
	if err != nil {
		return fmt.Errorf("wiki: temp file: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wiki: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("wiki: close: %w", err)
	}
	if err := os.Rename(name, dst); err != nil {
		return fmt.Errorf("wiki: rename: %w", err)
	}
	return nil
}

// Read loads one page by ID.
func (s *Store) Read(_ context.Context, id ulid.ULID) (entities.WikiPage, error) {
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(Path(id))))
	if err != nil {
		return entities.WikiPage{}, err
	}
	return Decode(b)
}

// List returns every page, newest first.
func (s *Store) List(_ context.Context) ([]entities.WikiPage, error) {
	var out []entities.WikiPage
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != s.root {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		page, err := Decode(b)
		if err != nil {
			return nil
		}
		out = append(out, page)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("wiki: list: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	return out, nil
}

// Remove moves a page's file to the vault's trash. The page is user-authored: deleting it from the
// lens must not be the one operation that destroys bytes with no way back.
func (s *Store) Remove(_ context.Context, id ulid.ULID) error {
	src := filepath.Join(s.root, filepath.FromSlash(Path(id)))
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("wiki: remove: %w", err)
	}
	trash := filepath.Join(filepath.Dir(s.root), ".trash", "wiki")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return fmt.Errorf("wiki: trash dir: %w", err)
	}
	dst := filepath.Join(trash, id.String()+".md")
	_ = os.Remove(dst)
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("wiki: trash: %w", err)
	}
	return nil
}

// NormalizeTags lowercases, normalizes and deduplicates tags, preserving their first-seen order.
func NormalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		t := coremarkdown.NormalizeTag(tag)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
