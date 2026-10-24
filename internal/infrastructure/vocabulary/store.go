// Package vocabulary stores the agent's canonical name vocabularies in the vault: one Markdown file per
// namespace under <vault>/vocabulary/.
//
// It lives in the VAULT, not in the index, because it is a DECISION and not an observation. The index
// knows which names exist right now (it can be rebuilt from the atoms and pages); it cannot know that
// "cache-invalidation" was merged into "caching" — that is what the agent decided, and losing it would
// mean the next curation asks the same question again and a write with the old name starts a new
// name. So the file is the truth, it is readable and greppable like the atoms, and a reindex reads it
// back.
package vocabulary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// Ext is the file extension of a vocabulary file (<namespace>.md).
const Ext = ".md"

// Store reads and writes the vocabulary files of one vault.
type Store struct {
	root string
}

// New returns a store rooted at the vault's vocabulary directory.
func New(root string) *Store { return &Store{root: root} }

// Path is a namespace's file.
func (s *Store) Path(ns entities.VocabularyNamespace) string {
	return filepath.Join(s.root, string(ns)+Ext)
}

// Read returns a namespace's vocabulary. A missing file is not an error: it is a vault whose names have
// never been curated, and the caller treats it as empty (every observed name is new).
func (s *Store) Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error) {
	b, err := os.ReadFile(s.Path(ns))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}, nil
		}
		return entities.Vocabulary{}, err
	}
	return parse(ns, string(b)), nil
}

// Write stores a namespace's vocabulary atomically (temp file → rename).
func (s *Store) Write(v entities.Vocabulary) error {
	if v.Namespace == "" {
		return fmt.Errorf("vocabulary: namespace is required")
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	path := s.Path(v.Namespace)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(render(v)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// render writes the file a reader can use: the canonical names first (grouped under their domain when
// they are paths, which is how a topic vocabulary reads), then what they absorbed.
func render(v entities.Vocabulary) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("kind: vocabulary\n")
	fmt.Fprintf(&b, "namespace: %s\n", v.Namespace)
	if !v.Updated.IsZero() {
		fmt.Fprintf(&b, "updated: %s\n", v.Updated.UTC().Format(time.RFC3339))
	}
	if v.Run != (ulid.ULID{}) {
		fmt.Fprintf(&b, "run: %s\n", v.Run.String())
	}
	b.WriteString("---\n\n")
	b.WriteString("# Canonical\n\n")

	// Grouped by the first path segment: for topics that is the domain (the umbrella the UI groups
	// by), for a flat namespace there is no group and the list is simply a list.
	groups := map[string][]string{}
	order := []string{}
	for _, name := range v.Canonical {
		g := ""
		if i := strings.IndexByte(name, '/'); i > 0 {
			g = name[:i]
		}
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], name)
	}
	for _, g := range order {
		if g != "" {
			fmt.Fprintf(&b, "## %s\n\n", g)
		}
		for _, name := range groups[g] {
			fmt.Fprintf(&b, "- %s\n", name)
		}
		b.WriteString("\n")
	}

	b.WriteString("# Decided\n\n")
	for _, name := range sortedStrings(v.Decided) {
		fmt.Fprintf(&b, "- %s\n", name)
	}
	b.WriteString("\n# Absorbed\n\n")
	for _, from := range sortedKeys(v.Absorbed) {
		fmt.Fprintf(&b, "- %s: %s\n", from, v.Absorbed[from])
	}
	b.WriteString("\n# Broad Tokens\n\n")
	for _, token := range sortedStrings(v.BroadTokens) {
		fmt.Fprintf(&b, "- %s\n", token)
	}
	return b.String()
}

// parse reads the file back. It is deliberately tolerant: the bullets under `# Canonical` are the
// canonical names (with or without a `## <domain>` heading above them, which is only a reading aid),
// and the bullets under `# Absorbed` are `old: new` pairs.
func parse(ns entities.VocabularyNamespace, text string) entities.Vocabulary {
	v := entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}
	part := ""
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line == "---":
			continue
		case strings.HasPrefix(line, "# "):
			part = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
			continue
		case strings.HasPrefix(line, "## "):
			continue // a reading aid for a path namespace
		case strings.HasPrefix(line, "kind:") || strings.HasPrefix(line, "namespace:"):
			continue
		case strings.HasPrefix(line, "updated:"):
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(line, "updated:"))); err == nil {
				v.Updated = t.UTC()
			}
			continue
		case strings.HasPrefix(line, "run:"):
			if id, err := ulid.ParseStrict(strings.TrimSpace(strings.TrimPrefix(line, "run:"))); err == nil {
				v.Run = id
			}
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if item == "" {
			continue
		}
		switch part {
		case "absorbed":
			from, to, ok := strings.Cut(item, ":")
			if !ok {
				continue
			}
			if from, to = strings.TrimSpace(from), strings.TrimSpace(to); from != "" && to != "" {
				v.Absorbed[from] = to
			}
		case "canonical":
			v.Canonical = append(v.Canonical, item)
		case "broad tokens":
			v.BroadTokens = append(v.BroadTokens, strings.ToLower(item))
		case "decided":
			// The names the model has already judged. Losing them re-opens the whole catalog on the
			// next pass: every chunk would write its own names as the only decided ones, and a
			// thousands-name catalogue would be re-asked every cooldown, forever.
			v.Decided = append(v.Decided, item)
		}
	}
	return v
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
