// Package tree stores a source's node tree as a JSON sidecar NEXT TO THE SOURCE ITSELF
// (sources/<ab>/<cd>/<hash>.outline). The outline is derived and rebuildable; it is never the source
// of truth. It lives with the source so that deleting the source deletes its outline too, and so that
// a source is one folder entry with everything about it.
package tree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Store persists outlines next to their sources (rooted at the vault's sources/ directory).
type Store struct {
	root string
}

// New returns an outline store over a sources/ directory.
func New(sourcesDir string) *Store { return &Store{root: sourcesDir} }

// Root is the sources directory this store works in.
func (s *Store) Root() string { return s.root }

// treeFile is the outline's own format: STRUCTURE and REFERENCES, never the text.
//
// A node's text lives in the source's readable rendering, and the outline says WHERE (TextStart/TextEnd)
// and WHAT it is (TextHash) — the outline of a 5 MB document must not be a second copy of it.
type treeFile struct {
	SourceHash string     `json:"source_hash"`
	Nodes      []nodeJSON `json:"nodes"`
}

type nodeJSON struct {
	NodeID    string `json:"node_id"`
	ParentID  string `json:"parent_id,omitempty"`
	Depth     int    `json:"depth"`
	Ord       int    `json:"ord"`
	Title     string `json:"title,omitempty"`
	Summary   string `json:"summary,omitempty"`
	StartPage int    `json:"start_page,omitempty"`
	EndPage   int    `json:"end_page,omitempty"`
	OCR       bool   `json:"ocr,omitempty"`
	TextStart int    `json:"text_start,omitempty"`
	TextEnd   int    `json:"text_end,omitempty"`
	TextHash  string `json:"text_hash,omitempty"`
	// Text is an outline written BEFORE the references existed: it still carries the text. It is read
	// (and its hash derived from it), never written again — the first rebuild replaces the file.
	Text string `json:"text,omitempty"`
}

func nodeToJSON(n entities.SourceNode) nodeJSON {
	j := nodeJSON{
		NodeID: n.NodeID, ParentID: n.ParentID, Depth: n.Depth, Ord: n.Ord,
		Title: n.Title, Summary: n.Summary, StartPage: n.StartPage, EndPage: n.EndPage,
		OCR: n.OCR, TextStart: n.TextStart, TextEnd: n.TextEnd, TextHash: n.TextHash,
	}
	// The text is written ONLY for a node that has no reference — a text that could not be found in
	// the readable text (an extractor that normalised it). A node that can be referenced is not
	// copied: that is the whole point of the reference.
	if n.TextEnd <= n.TextStart {
		j.Text = n.Text
	}
	return j
}

func (j nodeJSON) toNode() entities.SourceNode {
	n := entities.SourceNode{
		NodeID: j.NodeID, ParentID: j.ParentID, Depth: j.Depth, Ord: j.Ord,
		Title: j.Title, Summary: j.Summary, StartPage: j.StartPage, EndPage: j.EndPage,
		OCR: j.OCR, TextStart: j.TextStart, TextEnd: j.TextEnd, TextHash: j.TextHash,
	}
	if j.Text != "" {
		// An outline from an older build: it has the text, so the hash can be derived from it and the
		// tree comparison stays honest instead of invalidating a derivation for a format change.
		n.Text = j.Text
		n.TextHash = hashText(j.Text)
	}
	n.HasText = n.TextHash != "" || n.TextEnd > n.TextStart
	return n
}

func hashText(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Write atomically writes the outline sidecar for a source.
func (s *Store) Write(_ context.Context, sourceHash string, nodes []entities.SourceNode) error {
	dst := s.path(sourceHash)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("outline store: mkdir: %w", err)
	}
	shaped := make([]nodeJSON, 0, len(nodes))
	for _, n := range nodes {
		shaped = append(shaped, nodeToJSON(n))
	}
	b, err := json.Marshal(treeFile{SourceHash: sourceHash, Nodes: shaped})
	if err != nil {
		return fmt.Errorf("outline store: marshal: %w", err)
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("outline store: write: %w", err)
	}
	return os.Rename(tmp, dst)
}

// Read returns the outline for a source.
func (s *Store) Read(_ context.Context, sourceHash string) ([]entities.SourceNode, error) {
	b, err := os.ReadFile(s.path(sourceHash))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, types.New("not_found", "no outline for this source", types.ErrNotFound)
		}
		return nil, fmt.Errorf("outline store: read: %w", err)
	}
	var f treeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("outline store: unmarshal: %w", err)
	}
	out := make([]entities.SourceNode, 0, len(f.Nodes))
	for _, j := range f.Nodes {
		out = append(out, j.toNode())
	}
	return out, nil
}

// path is the outline sidecar of a source: <sources>/<ab>/<cd>/<hash>.outline.
func (s *Store) path(hash string) string {
	return objects.SidecarPath(s.root, hash, entities.SidecarOutline)
}

// Remove deletes a source's outline sidecar.
func (s *Store) Remove(_ context.Context, sourceHash string) error {
	if err := os.Remove(s.path(sourceHash)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("outline store: remove: %w", err)
	}
	return nil
}
