package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	mdcodec "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/markdown"
)

// DetectLegacyFolders reports what a vault still holds that is NOT at the canonical path: the topic
// folders (with how many claims they carry), a claim lying directly in atoms/ instead of its shard, and
// the generated index files beside them. Read-only — this is what tells a reader whether the reorganize
// has anything to do.
//
// A folder is the old layout when it carries a `.md` file DIRECTLY: that is what a topic folder was
// (one file per claim), and it is what a shard folder never is — `atoms/ad/8e/…` holds directories.
// The NAME is not consulted, and must not be: a folder whose claims are all gone keeps only its
// generated `index.md`, and its topic is named nowhere but in that folder — asking the index would
// mean never cleaning up exactly the folders that need it.
func (s *Store) DetectLegacyFolders() (folders []string, claims, indexes int, err error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, 0, nil
		}
		return nil, 0, 0, fmt.Errorf("atoms: read root: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			// A claim that lies directly in atoms/ instead of its shard: it is indexed (the scan reads
			// it), but it is not where the layout puts it. Counted here, moved by Reorganize.
			if strings.HasSuffix(e.Name(), ".md") && e.Name() != "index.md" {
				claims++
			}
			continue
		}
		inner, err := os.ReadDir(filepath.Join(s.root, e.Name()))
		if err != nil {
			continue
		}
		found := 0
		idx := 0
		for _, f := range inner {
			if f.IsDir() {
				continue
			}
			switch {
			case f.Name() == "index.md":
				// The generated topic index of the old layout: a claim list whose claims are the files
				// beside it. Rebuildable, and the new layout has no place for it.
				idx++
			case strings.HasSuffix(f.Name(), ".md"):
				found++
			}
		}
		if found == 0 && idx == 0 {
			// No claim and no index: not a topic folder (a shard, or a directory of something else).
			continue
		}
		folders = append(folders, e.Name())
		claims += found
		indexes += idx
	}
	return folders, claims, indexes, nil
}

// Reorganize moves every atom out of those folders to its canonical path (`<ab>/<cd>/<id>.md`), takes
// its usage record along, and removes the folder once nothing is left in it. It reports what it did:
// the claims it moved, the folders it removed, the generated topic indexes it deleted, the files it
// could not read (an atom is never thrown away because it could not be parsed — such a file stays
// where it is and the folder stays with it), and the folders that still hold something.
//
// The indexes go because they cannot be kept: they catalog a FOLDER, and there are no topic folders
// any more. `atoms/index.md` — the topic catalog — stays and is rewritten from the index, so the
// vault keeps the one index that still describes something.
// The atoms it moved are RETURNED, not only counted: the index has to learn their new paths before
// anything writes an atom's usage record — a record is written beside its atom, so a stale path in the
// index recreates the very folder this move just removed.
func (s *Store) Reorganize(ctx context.Context) (moved []entities.Atom, folders, indexes, skipped int, kept []string, err error) {
	legacy, _, _, err := s.DetectLegacyFolders()
	if err != nil {
		return nil, 0, 0, 0, nil, err
	}
	// A claim directly in atoms/ is moved like one in a topic folder: every claim ends at its
	// canonical path, wherever it was found.
	if entries, err := os.ReadDir(s.root); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "index.md" {
				continue
			}
			path := filepath.Join(s.root, e.Name())
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				skipped++
				continue
			}
			a, decErr := mdcodec.DecodeAtom(data)
			if decErr != nil {
				skipped++
				continue
			}
			stored, writeErr := s.Write(ctx, a)
			if writeErr != nil {
				return moved, folders, indexes, skipped, kept, writeErr
			}
			moved = append(moved, stored)
			if err := os.Remove(path); err != nil {
				return moved, folders, indexes, skipped, kept, fmt.Errorf("atoms: remove moved file: %w", err)
			}
		}
	}
	for _, topic := range legacy {
		dir := filepath.Join(s.root, topic)
		entries, err := os.ReadDir(dir)
		if err != nil {
			skipped++
			continue
		}
		_ = entries
		for _, e := range entries {
			src := filepath.Join(dir, e.Name())
			if e.IsDir() {
				kept = append(kept, topic)
				continue
			}
			// The generated index of the old layout has no home in the new one: the topic is a field
			// now, and atoms/index.md carries the topic list. Its claims are the files beside it, so
			// nothing is lost by removing it — and it is COUNTED, never silently dropped.
			if e.Name() == "index.md" {
				if removeErr := os.Remove(src); removeErr != nil {
					return moved, folders, indexes, skipped, kept, fmt.Errorf("atoms: remove index: %w", removeErr)
				}
				indexes++
				continue
			}
			if !strings.HasSuffix(e.Name(), ".md") {
				kept = append(kept, topic)
				continue
			}
			data, readErr := os.ReadFile(src)
			if readErr != nil {
				skipped++
				continue
			}
			a, decErr := mdcodec.DecodeAtom(data)
			if decErr != nil {
				// Not an atom: it stays where it is, and so does its folder.
				skipped++
				kept = append(kept, topic)
				continue
			}
			// A vault written before the topic was a field kept it in the FOLDER: that name is the
			// only place it can come from here (a claim file never carried it).
			if a.Topic == "" {
				a.Topic = topic
			}
			stored, writeErr := s.Write(ctx, a)
			if writeErr != nil {
				return moved, folders, indexes, skipped, kept, writeErr
			}
			moved = append(moved, stored)
			if removeErr := os.Remove(src); removeErr != nil {
				return moved, folders, indexes, skipped, kept, fmt.Errorf("atoms: remove moved file: %w", removeErr)
			}
		}
		if rest, err := os.ReadDir(dir); err == nil && len(rest) == 0 {
			if err := os.Remove(dir); err == nil {
				folders++
				kept = without(kept, topic)
			}
		}
	}
	return moved, folders, indexes, skipped, kept, nil
}

func without(list []string, name string) []string {
	out := list[:0]
	for _, s := range list {
		if s != name {
			out = append(out, s)
		}
	}
	return out
}
