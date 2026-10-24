// Package asks stores the Ask view's conversations as Markdown records in the vault.
//
// An Ask is not a page: the cluster layer groups claims, while an ask is a question and the
// answer drawn from claims. It lives in its own vault directory (asks/), is written automatically
// when a query succeeds, and is removed only by the reader.
package asks

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
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Ext is the record's extension: <id>.md (YAML frontmatter + answer body).
const Ext = ".md"

type askFrontmatter struct {
	ID       string   `yaml:"id"`
	Question string   `yaml:"question"`
	Tags     []string `yaml:"tags,omitempty"`
	Created  string   `yaml:"created"`
	Updated  string   `yaml:"updated"`
}

// Store reads and writes the asks/ directory of one vault.
type Store struct {
	root string
}

// New returns a store rooted at the vault's asks directory.
func New(root string) *Store { return &Store{root: root} }

// Root is the asks directory.
func (s *Store) Root() string { return s.root }

// Path is the vault-relative file for one ask.
func Path(id ulid.ULID) string { return id.String() + Ext }

// Write stores one ask atomically.
func (s *Store) Write(_ context.Context, a entities.Ask) error {
	if a.ID == (ulid.ULID{}) {
		return fmt.Errorf("asks: an ask needs an id")
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("asks: mkdir: %w", err)
	}
	b, err := encode(a)
	if err != nil {
		return err
	}
	dst := filepath.Join(s.root, Path(a.ID))
	tmp, err := os.CreateTemp(s.root, ".ask-*.tmp")
	if err != nil {
		return fmt.Errorf("asks: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("asks: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("asks: close: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("asks: rename: %w", err)
	}
	return nil
}

// Get returns one ask.
func (s *Store) Get(_ context.Context, id ulid.ULID) (entities.Ask, error) {
	b, err := os.ReadFile(filepath.Join(s.root, Path(id)))
	if errors.Is(err, fs.ErrNotExist) {
		return entities.Ask{}, types.New("not_found", "no such ask", types.ErrNotFound)
	}
	if err != nil {
		return entities.Ask{}, fmt.Errorf("asks: read: %w", err)
	}
	return decode(b)
}

// List returns asks newest first. A malformed file is skipped: one bad record must not hide the
// rest of the saved searches.
func (s *Store) List(_ context.Context, limit, offset int) ([]entities.Ask, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return []entities.Ask{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asks: list: %w", err)
	}
	out := make([]entities.Ask, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, rErr := os.ReadFile(filepath.Join(s.root, e.Name()))
		if rErr != nil {
			continue
		}
		a, dErr := decode(b)
		if dErr != nil {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if offset >= len(out) {
		return []entities.Ask{}, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Delete moves an ask to .trash/asks/; a record is recoverable, like every deleted file in the vault.
func (s *Store) Delete(_ context.Context, id ulid.ULID) error {
	src := filepath.Join(s.root, Path(id))
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return types.New("not_found", "no such ask", types.ErrNotFound)
	}
	dir := filepath.Join(filepath.Dir(s.root), ".trash", "asks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("asks: trash dir: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	dst := filepath.Join(dir, stamp+"-"+Path(id))
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("asks: trash: %w", err)
	}
	return nil
}

// Normalize rewrites old saved answers that still carry the model's [[claim:...]] citation syntax.
// The raw file is meant to be readable Markdown, so the citation is turned into a real link.
func (s *Store) Normalize(_ context.Context) (int, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("asks: normalize: %w", err)
	}
	changed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(s.root, e.Name())
		b, rErr := os.ReadFile(path)
		if rErr != nil {
			continue
		}
		a, dErr := decode(b)
		if dErr != nil {
			continue
		}
		next := coremarkdown.NormalizeAskAnswer(a.Answer)
		if next == a.Answer {
			continue
		}
		a.Answer = next
		if wErr := s.Write(context.Background(), a); wErr != nil {
			return changed, wErr
		}
		changed++
	}
	return changed, nil
}

func encode(a entities.Ask) ([]byte, error) {
	fm := askFrontmatter{
		ID: a.ID.String(), Question: a.Question, Tags: a.Tags,
		Created: a.CreatedAt.UTC().Format(time.RFC3339), Updated: a.UpdatedAt.UTC().Format(time.RFC3339),
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("asks: encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("asks: close frontmatter: %w", err)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(a.Answer))
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func decode(b []byte) (entities.Ask, error) {
	text := string(b)
	if !strings.HasPrefix(text, "---\n") {
		return entities.Ask{}, fmt.Errorf("asks: missing frontmatter")
	}
	rest := text[4:]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return entities.Ask{}, fmt.Errorf("asks: unterminated frontmatter")
	}
	var fm askFrontmatter
	if err := yaml.Unmarshal([]byte(before), &fm); err != nil {
		return entities.Ask{}, fmt.Errorf("asks: decode frontmatter: %w", err)
	}
	id, err := ulid.ParseStrict(strings.TrimSpace(fm.ID))
	if err != nil {
		return entities.Ask{}, fmt.Errorf("asks: invalid id: %w", err)
	}
	created, _ := time.Parse(time.RFC3339, fm.Created)
	updated, _ := time.Parse(time.RFC3339, fm.Updated)
	body := strings.TrimSpace(after)
	return entities.Ask{ID: id, Question: fm.Question, Answer: body, Tags: fm.Tags, CreatedAt: created, UpdatedAt: updated}, nil
}
