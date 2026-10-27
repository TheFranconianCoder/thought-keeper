// Package atoms stores atoms as Markdown files addressed by their ID — as a source is addressed by
// its hash — and writes the generated topic index.
//
// The layout is `atoms/<ab>/<cd>/<id>.md`, sharded two levels deep like the source store, and the
// shard comes from the RANDOM part of the ULID: its first ten characters are a timestamp, so
// sharding by those would put every claim of one run into a single folder. The topic is a FIELD
// (`topic:` in the frontmatter), not a folder: it is a name the agent assigns and may revise, while
// the ID is the atom's identity — and a file that is addressed by its identity cannot be lost by a
// rename.
package atoms

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/idpath"
	mdcodec "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/markdown"
)

// Store is the atom file store rooted at the vault's atoms/ directory.
type Store struct {
	root string
}

// New returns a store rooted at root.
func New(root string) *Store { return &Store{root: root} }

// Shard is the two-level folder an atom lives in, derived from the TAIL of its ULID — the part that
// changes from one claim to the next. A ULID's first ten characters are a timestamp, and `ulid.Make`
// draws from a MONOTONIC entropy source, so a shard taken from the head would put every claim of one
// run into a single folder (measured: 200 claims, 2 folders).
func Shard(id ulid.ULID) string {
	return idpath.Shard(id)
}

// Path is an atom's vault-relative path: `<ab>/<cd>/<id>.md`.
func Path(id ulid.ULID) string {
	return filepath.ToSlash(filepath.Join(Shard(id), id.String()+".md"))
}

// Write stores the atom file at its canonical path and returns the persisted atom. Writing the same
// id again overwrites its file — one id is one file, exactly as one hash is one object.
func (s *Store) Write(ctx context.Context, a entities.Atom) (entities.Atom, error) {
	a.Topic = coremarkdown.NormalizeTopic(a.Topic)
	if a.Topic == "" {
		a.Topic = "inbox"
	}
	a.Path = Path(a.ID)
	return s.writePath(a)
}

// WritePath stores the atom at the path it records, so a repair rewrites a claim in place — a vault
// written before the ID layout keeps its file where it is instead of gaining a second copy.
func (s *Store) WritePath(_ context.Context, a entities.Atom) (entities.Atom, error) {
	a.Topic = coremarkdown.NormalizeTopic(a.Topic)
	if a.Topic == "" {
		a.Topic = "inbox"
	}
	if a.Path == "" {
		a.Path = Path(a.ID)
	}
	return s.writePath(a)
}

func (s *Store) writePath(a entities.Atom) (entities.Atom, error) {
	b, err := mdcodec.EncodeAtom(a)
	if err != nil {
		return entities.Atom{}, err
	}
	dst := filepath.Join(s.root, filepath.FromSlash(a.Path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return entities.Atom{}, fmt.Errorf("atoms: mkdir: %w", err)
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return entities.Atom{}, fmt.Errorf("atoms: write: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return entities.Atom{}, fmt.Errorf("atoms: rename: %w", err)
	}
	return a, nil
}

// Read loads an atom by its id.
func (s *Store) Read(ctx context.Context, id ulid.ULID) (entities.Atom, error) {
	return s.ReadPath(ctx, Path(id))
}

// ReadPath loads an atom from a path relative to the atom store's root — the path the index recorded,
// which is how a claim in the old folder layout is still found.
func (s *Store) ReadPath(_ context.Context, path string) (entities.Atom, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(path)))
	if err != nil {
		return entities.Atom{}, err
	}
	return mdcodec.DecodeAtom(data)
}

// Scan walks the atom store and returns every atom. Generated index files are skipped, and so is
// every file whose path is not an atom's canonical one — a vault that still holds the old topic
// folders is read from there (that is what makes the reorganize possible at all).
func (s *Store) Scan(_ context.Context) ([]entities.Atom, error) {
	var out []entities.Atom
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
		name := d.Name()
		if !strings.HasSuffix(name, ".md") || name == "index.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		a, err := mdcodec.DecodeAtom(data)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		a.Path = filepath.ToSlash(rel)
		if a.Topic == "" {
			a.Topic = "inbox"
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("atoms: scan: %w", err)
	}
	return out, nil
}

// Remove deletes an atom's Markdown file.
func (s *Store) Remove(_ context.Context, a entities.Atom) error {
	if a.Path == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(s.root, filepath.FromSlash(a.Path))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("atoms: remove: %w", err)
	}
	return nil
}

// RemoveStaleIndex deletes a generated atoms/index.md left by an older build. The topic catalog is not
// written any more: the index is SQLite's job, and a second catalog is one more thing to keep in step
// with it. A file that is not there is not an error — that is the normal case now.
func (s *Store) RemoveStaleIndex() (bool, error) {
	path := filepath.Join(s.root, "index.md")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("atoms: remove stale index: %w", err)
	}
	return true, nil
}
