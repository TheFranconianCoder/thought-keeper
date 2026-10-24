// Package markdown decodes and encodes note/atom files (YAML frontmatter + body).
// YAML lives here, never in the core.
package markdown

import (
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"gopkg.in/yaml.v3"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
)

type atomFrontmatter struct {
	ID      string           `yaml:"id"`
	Claim   string           `yaml:"claim"`
	Topic   string           `yaml:"topic"`
	Type    string           `yaml:"type,omitempty"`
	Status  string           `yaml:"status,omitempty"`
	Sources []atomSourceYAML `yaml:"sources,omitempty"`
	Created string           `yaml:"created,omitempty"`
	Updated string           `yaml:"updated,omitempty"`
}

type atomSourceYAML struct {
	Hash string `yaml:"hash"`
	Node string `yaml:"node,omitempty"`
	OCR  bool   `yaml:"ocr,omitempty"`
}

// EncodeAtom renders an atom as a Markdown file (frontmatter + body).
func EncodeAtom(a entities.Atom) ([]byte, error) {
	fm := atomFrontmatter{
		ID:      a.ID.String(),
		Claim:   a.Claim,
		Topic:   a.Topic,
		Type:    string(a.Type),
		Status:  string(a.Status),
		Created: a.CreatedAt.UTC().Format(time.RFC3339),
		Updated: a.UpdatedAt.UTC().Format(time.RFC3339),
	}
	for _, s := range a.Sources {
		fm.Sources = append(fm.Sources, atomSourceYAML{Hash: s.Hash, Node: s.NodeID, OCR: s.OCR})
	}

	var b strings.Builder
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close frontmatter: %w", err)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(a.Body))
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// DecodeAtom parses an atom file into a domain entity.
func DecodeAtom(data []byte) (entities.Atom, error) {
	fmBytes, body := splitFrontmatter(string(data))
	if fmBytes == nil {
		return entities.Atom{}, fmt.Errorf("atom file has no frontmatter")
	}
	var fm atomFrontmatter
	if err := yaml.Unmarshal([]byte(*fmBytes), &fm); err != nil {
		return entities.Atom{}, fmt.Errorf("decode frontmatter: %w", err)
	}
	var a entities.Atom
	if fm.ID != "" {
		id, err := ulid.ParseStrict(fm.ID)
		if err != nil {
			return entities.Atom{}, fmt.Errorf("invalid atom id %q: %w", fm.ID, err)
		}
		a.ID = id
	}
	a.Claim = fm.Claim
	a.Topic = fm.Topic
	a.Type = entities.AtomType(fm.Type)
	a.Status = entities.AtomStatus(fm.Status)
	for _, s := range fm.Sources {
		a.Sources = append(a.Sources, entities.AtomSource{Hash: s.Hash, NodeID: s.Node, OCR: s.OCR})
	}
	if t, err := time.Parse(time.RFC3339, fm.Created); err == nil {
		a.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, fm.Updated); err == nil {
		a.UpdatedAt = t
	}
	a.Body = strings.TrimRight(body, "\n")
	for _, l := range coremarkdown.Wikilinks(a.Body) {
		a.Links = append(a.Links, entities.Link{RawTarget: l.RawTarget, Alias: l.Alias, Heading: l.Heading, Position: l.Position})
	}
	return a, nil
}

func splitFrontmatter(s string) (frontmatter *string, body string) {
	if !strings.HasPrefix(s, "---\n") {
		return nil, s
	}
	rest := s[len("---\n"):]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return nil, s
	}
	fm := before
	body = after
	body = strings.TrimPrefix(body, "\n")
	return &fm, body
}
