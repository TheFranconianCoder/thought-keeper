// Package commands stores the assistant's commands as Markdown records in the vault.
//
// A command is not a page: it is one utterance, the criteria it was understood as, and the set it
// produced — the state the UI was asked to show. It is written once and never edited; a second
// request is a second command. The records are deliberately short-lived: the consistency job prunes
// them, which is why an address that names one is an address with an expiry date.
package commands

import (
	"bytes"
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
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Ext is the record's extension: <id>.md (YAML frontmatter + a human-readable set).
const Ext = ".md"

type mappedFrontmatter struct {
	Name  string `yaml:"name"`
	Kind  string `yaml:"kind"`
	Count int    `yaml:"count,omitempty"`
	Found int    `yaml:"found,omitempty"`
}

type itemFrontmatter struct {
	Ref   string `yaml:"ref"`
	Title string `yaml:"title,omitempty"`
	Kind  string `yaml:"kind,omitempty"`
	Why   string `yaml:"why,omitempty"`
}

type criteriaFrontmatter struct {
	Text          string   `yaml:"text,omitempty"`
	Tag           string   `yaml:"tag,omitempty"`
	Kind          string   `yaml:"kind,omitempty"`
	Topic         string   `yaml:"topic,omitempty"`
	Tags          []string `yaml:"tags,omitempty"`
	Topics        []string `yaml:"topics,omitempty"`
	FromTopic     string   `yaml:"from_topic,omitempty"`
	Source        string   `yaml:"source,omitempty"`
	Atom          string   `yaml:"atom,omitempty"`
	Since         string   `yaml:"since,omitempty"`
	State         string   `yaml:"state,omitempty"`
	Order         string   `yaml:"order,omitempty"`
	BookmarksOnly bool     `yaml:"bookmarks_only,omitempty"`
	AttachedOnly  bool     `yaml:"attached_only,omitempty"`
}

type commandFrontmatter struct {
	ID         string              `yaml:"id"`
	Text       string              `yaml:"text"`
	Lens       string              `yaml:"lens"`
	URL        string              `yaml:"url"`
	Resolution string              `yaml:"resolution,omitempty"`
	Rationale  string              `yaml:"rationale,omitempty"`
	Notes      []string            `yaml:"notes,omitempty"`
	Generated  string              `yaml:"generated,omitempty"`
	Total      int                 `yaml:"total"`
	Vault      int                 `yaml:"vault,omitempty"`
	Scanned    int                 `yaml:"scanned,omitempty"`
	Created    string              `yaml:"created"`
	Criteria   criteriaFrontmatter `yaml:"criteria"`
	Mapped     []mappedFrontmatter `yaml:"mapped,omitempty"`
	Items      []itemFrontmatter   `yaml:"items,omitempty"`
}

// Store reads and writes the commands/ directory of one vault.
type Store struct {
	root string
}

// New returns a store rooted at the vault's commands directory.
func New(root string) *Store { return &Store{root: root} }

// Root is the commands directory.
func (s *Store) Root() string { return s.root }

// Path is the vault-relative file for one command.
func Path(id ulid.ULID) string { return id.String() + Ext }

// Write stores one command atomically.
func (s *Store) Write(_ context.Context, c entities.Command) error {
	if c.ID == (ulid.ULID{}) {
		return fmt.Errorf("commands: a command needs an id")
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("commands: mkdir: %w", err)
	}
	b, err := encode(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".command-*.tmp")
	if err != nil {
		return fmt.Errorf("commands: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("commands: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("commands: close: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.root, Path(c.ID))); err != nil {
		return fmt.Errorf("commands: rename: %w", err)
	}
	return nil
}

// Get returns one command.
func (s *Store) Get(_ context.Context, id ulid.ULID) (entities.Command, error) {
	b, err := os.ReadFile(filepath.Join(s.root, Path(id)))
	if errors.Is(err, fs.ErrNotExist) {
		return entities.Command{}, types.New("not_found", "no such command", types.ErrNotFound)
	}
	if err != nil {
		return entities.Command{}, fmt.Errorf("commands: read: %w", err)
	}
	return decode(b)
}

// List returns commands newest first. A malformed file is skipped: one bad record must not hide the
// rest of the history.
func (s *Store) List(_ context.Context, limit, offset int) ([]entities.Command, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return []entities.Command{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commands: list: %w", err)
	}
	out := make([]entities.Command, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, rErr := os.ReadFile(filepath.Join(s.root, e.Name()))
		if rErr != nil {
			continue
		}
		c, dErr := decode(b)
		if dErr != nil {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset >= len(out) {
		return []entities.Command{}, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Delete moves a command to .trash/commands/. A record is recoverable, like every deleted file.
func (s *Store) Delete(_ context.Context, id ulid.ULID) error {
	src := filepath.Join(s.root, Path(id))
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return types.New("not_found", "no such command", types.ErrNotFound)
	}
	return s.trash(src, Path(id))
}

// Prune moves every command created before the cutoff to the trash.
func (s *Store) Prune(_ context.Context, before time.Time) (int, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("commands: prune: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(s.root, e.Name())
		b, rErr := os.ReadFile(path)
		if rErr != nil {
			continue
		}
		c, dErr := decode(b)
		if dErr != nil || !c.CreatedAt.Before(before) {
			continue
		}
		if tErr := s.trash(path, e.Name()); tErr != nil {
			return removed, tErr
		}
		removed++
	}
	return removed, nil
}

func (s *Store) trash(src, name string) error {
	dir := filepath.Join(filepath.Dir(s.root), ".trash", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("commands: trash dir: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	if err := os.Rename(src, filepath.Join(dir, stamp+"-"+name)); err != nil {
		return fmt.Errorf("commands: trash: %w", err)
	}
	return nil
}

func encode(c entities.Command) ([]byte, error) {
	mapped := make([]mappedFrontmatter, 0, len(c.Mapped))
	for _, m := range c.Mapped {
		mapped = append(mapped, mappedFrontmatter{Name: m.Name, Kind: m.Kind, Count: m.Count, Found: m.Found})
	}
	items := make([]itemFrontmatter, 0, len(c.Items))
	for _, it := range c.Items {
		items = append(items, itemFrontmatter{Ref: it.Ref, Title: it.Title, Kind: it.Kind, Why: it.Why})
	}
	fm := commandFrontmatter{
		ID: c.ID.String(), Text: c.Text, Lens: string(c.Lens), URL: c.URL,
		Resolution: c.Resolution, Rationale: c.Rationale, Notes: c.Notes,
		Generated: c.Generated, Total: c.Total, Vault: c.Vault, Scanned: c.Scanned,
		Created: c.CreatedAt.UTC().Format(time.RFC3339),
		Criteria: criteriaFrontmatter{
			Text: c.Criteria.Text, Tag: c.Criteria.Tag, Kind: c.Criteria.Kind,
			Topic: c.Criteria.Topic, FromTopic: c.Criteria.FromTopic,
			Tags: c.Criteria.Tags, Topics: c.Criteria.Topics,
			Source: c.Criteria.Source, Atom: c.Criteria.Atom,
			Since: durationString(c.Criteria.Since),
			State: string(c.Criteria.State), Order: string(c.Criteria.Order),
			BookmarksOnly: c.Criteria.BookmarksOnly, AttachedOnly: c.Criteria.AttachedOnly,
		},
		Mapped: mapped,
		Items:  items,
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("commands: encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("commands: close frontmatter: %w", err)
	}
	b.WriteString("---\n\n")
	b.WriteString(renderBody(c))
	return b.Bytes(), nil
}

// renderBody writes what a human sees when they open the record: what was asked, what it meant, and
// what it found. The list rows themselves are never copied — a ref and a title are enough.
func renderBody(c entities.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(c.Text))
	if c.Rationale != "" {
		fmt.Fprintf(&b, "%s\n\n", c.Rationale)
	}
	for _, m := range c.Mapped {
		fmt.Fprintf(&b, "- %s %s (%d in the vault, %d here)\n", m.Kind, m.Name, m.Count, m.Found)
	}
	if len(c.Mapped) > 0 {
		b.WriteString("\n")
	}
	for _, note := range c.Notes {
		fmt.Fprintf(&b, "- %s\n", note)
	}
	if len(c.Notes) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%d of %d shown", len(c.Items), c.Total)
	if c.Scanned > 0 && c.Vault > c.Scanned {
		fmt.Fprintf(&b, " (scanned the newest %d of %d)", c.Scanned, c.Vault)
	}
	b.WriteString("\n\n")
	for _, it := range c.Items {
		fmt.Fprintf(&b, "- %s — `%s`", it.Title, it.Ref)
		if it.Why != "" {
			fmt.Fprintf(&b, " · %s", it.Why)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func decode(b []byte) (entities.Command, error) {
	text := string(b)
	if !strings.HasPrefix(text, "---\n") {
		return entities.Command{}, fmt.Errorf("commands: missing frontmatter")
	}
	before, _, ok := strings.Cut(text[4:], "\n---")
	if !ok {
		return entities.Command{}, fmt.Errorf("commands: unterminated frontmatter")
	}
	var fm commandFrontmatter
	if err := yaml.Unmarshal([]byte(before), &fm); err != nil {
		return entities.Command{}, fmt.Errorf("commands: decode frontmatter: %w", err)
	}
	id, err := ulid.ParseStrict(strings.TrimSpace(fm.ID))
	if err != nil {
		return entities.Command{}, fmt.Errorf("commands: invalid id: %w", err)
	}
	created, _ := time.Parse(time.RFC3339, fm.Created)
	mapped := make([]entities.CommandName, 0, len(fm.Mapped))
	for _, m := range fm.Mapped {
		mapped = append(mapped, entities.CommandName{Name: m.Name, Kind: m.Kind, Count: m.Count, Found: m.Found})
	}
	items := make([]entities.CommandItem, 0, len(fm.Items))
	for _, it := range fm.Items {
		items = append(items, entities.CommandItem{Ref: it.Ref, Title: it.Title, Kind: it.Kind, Why: it.Why})
	}
	return entities.Command{
		ID: id, Text: fm.Text, Lens: entities.CommandLens(fm.Lens), URL: fm.URL,
		Resolution: fm.Resolution, Rationale: fm.Rationale, Notes: fm.Notes,
		Generated: fm.Generated, Total: fm.Total, Vault: fm.Vault, Scanned: fm.Scanned,
		CreatedAt: created,
		Criteria: entities.CommandCriteria{
			Text: fm.Criteria.Text, Tag: fm.Criteria.Tag, Kind: fm.Criteria.Kind,
			Topic: fm.Criteria.Topic, FromTopic: fm.Criteria.FromTopic,
			Tags: fm.Criteria.Tags, Topics: fm.Criteria.Topics,
			Source: fm.Criteria.Source, Atom: fm.Criteria.Atom,
			Since: parseDuration(fm.Criteria.Since),
			State: entities.CommandState(fm.Criteria.State), Order: entities.CommandOrder(fm.Criteria.Order),
			BookmarksOnly: fm.Criteria.BookmarksOnly, AttachedOnly: fm.Criteria.AttachedOnly,
		},
		Mapped: mapped,
		Items:  items,
	}, nil
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
