// Package objects implements the content-addressed source store: objects named by
// their SHA-256, sharded into subdirectories, write-once and deduplicated.
package objects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Store is a content-addressed store rooted at a directory (the vault's sources/).
type Store struct {
	root  string
	trash string
}

// New returns a store rooted at root. Removed objects are moved next to the vault
// (root's parent) under .trash/objects/.
func New(root string) *Store {
	return &Store{root: root, trash: filepath.Join(filepath.Dir(root), ".trash", "objects")}
}

// Put hashes r and stores it as <root>/<ab>/<cd>/<sha256><ext>. Storing existing
// bytes is a no-op that returns the existing object (deduplication).
func (s *Store) Put(_ context.Context, ext string, r io.Reader) (entities.Object, error) {
	ext = strings.ToLower(ext)
	if ext == "" || !strings.HasPrefix(ext, ".") {
		return entities.Object{}, types.New("unsupported_source", "missing or invalid extension", types.ErrUnsupportedSource)
	}

	tmpDir := filepath.Join(s.root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return entities.Object{}, fmt.Errorf("objects: create tmp dir: %w", err)
	}
	f, err := os.CreateTemp(tmpDir, "intake-*")
	if err != nil {
		return entities.Object{}, fmt.Errorf("objects: temp file: %w", err)
	}
	tmpName := f.Name()
	defer func() { _ = os.Remove(tmpName) }()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		_ = f.Close()
		return entities.Object{}, fmt.Errorf("objects: write: %w", err)
	}
	if err := f.Close(); err != nil {
		return entities.Object{}, fmt.Errorf("objects: close: %w", err)
	}

	hash := hex.EncodeToString(h.Sum(nil))
	rel := relPath(hash, ext)
	dst := filepath.Join(s.root, rel)

	if _, err := os.Stat(dst); err == nil {
		// Already stored: deduplicate.
		return entities.Object{Hash: hash, Ext: ext, Path: rel, Bytes: n}, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return entities.Object{}, fmt.Errorf("objects: create shard dir: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return entities.Object{}, fmt.Errorf("objects: rename: %w", err)
	}
	return entities.Object{Hash: hash, Ext: ext, Path: rel, Bytes: n}, nil
}

// PutAt writes bytes at a FIXED address, replacing whatever is there. It exists for the two kinds of
// source whose content MOVES: a URL snapshot and a wiki page representation.
//
// Their identity cannot be the bytes' hash — the bytes are a snapshot of something that can change,
// and the address is what has to stay. Every other object is content-addressed and write-once
// (`Put`); a mutable source keeps the address it was created with and is refreshed in place, which
// is what makes an edit update ONE source instead of filing a second one beside it.
func (s *Store) PutAt(hash, ext string, content []byte) (entities.Object, error) {
	if !entities.IsSHA256Hex(hash) {
		return entities.Object{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	ext = strings.ToLower(ext)
	if ext == "" || !strings.HasPrefix(ext, ".") {
		return entities.Object{}, types.New("unsupported_source", "missing or invalid extension", types.ErrUnsupportedSource)
	}
	rel := relPath(hash, ext)
	dst := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return entities.Object{}, fmt.Errorf("objects: create shard dir: %w", err)
	}
	if err := writeAtomic(dst, content); err != nil {
		return entities.Object{}, err
	}
	return entities.Object{Hash: hash, Ext: ext, Path: rel, Bytes: int64(len(content))}, nil
}

// Open returns the bytes of an object.
func (s *Store) Open(hash, ext string) (io.ReadCloser, error) {
	if !entities.IsSHA256Hex(hash) {
		return nil, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	return os.Open(filepath.Join(s.root, relPath(hash, strings.ToLower(ext))))
}

// Remove moves an object out of the store into the trash. Trash, not delete: the
// bytes stay recoverable while the source no longer exists for the index.
func (s *Store) Remove(_ context.Context, hash, ext string) error {
	if !entities.IsSHA256Hex(hash) {
		return types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src := filepath.Join(s.root, relPath(hash, strings.ToLower(ext)))
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("objects: stat: %w", err)
	}
	if err := os.MkdirAll(s.trash, 0o755); err != nil {
		return fmt.Errorf("objects: trash dir: %w", err)
	}
	dst := filepath.Join(s.trash, hash+strings.ToLower(ext))
	_ = os.Remove(dst)
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("objects: trash: %w", err)
	}
	return nil
}

// SidecarPath is where a derived file of a source lives: <root>/<ab>/<cd>/<hash><suffix>, next to
// the object it belongs to. One rule for all of them (rendering, step record, outline, log), so the
// layout cannot drift between the stores that write them.
func SidecarPath(root, hash, suffix string) string {
	if len(hash) < 4 || !entities.IsSHA256Hex(hash) {
		return filepath.Join(root, hash+suffix)
	}
	return filepath.Join(root, hash[:2], hash[2:4], hash+suffix)
}

// StatObject returns the object's size and whether it exists. The object IS the source: without it
// there is nothing to derive from, and no reindex can bring it back.
// DeclaredURL reads the URL a source's object declares in its YAML header (see
// markdown.SourceDeclaredURL). Only the head of the file is read: the header is at the top, and a
// snapshot can be megabytes.
func (s *Store) DeclaredURL(_ context.Context, hash, ext string) (string, error) {
	if !entities.IsSHA256Hex(hash) {
		return "", types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	f, err := os.Open(filepath.Join(s.root, relPath(hash, strings.ToLower(ext))))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 8<<10)
	n, _ := io.ReadFull(f, head)
	return markdown.SourceDeclaredURL(string(head[:n])), nil
}

func (s *Store) StatObject(hash, ext string) (int64, bool) {
	if !entities.IsSHA256Hex(hash) {
		return 0, false
	}
	return statFile(filepath.Join(s.root, relPath(hash, strings.ToLower(ext))))
}

// StatSidecar returns the size of one of a source's derived files and whether it exists. It is how
// the consistency job notices that a step's artefact is gone (an outline deleted by accident) while
// the record still claims the step succeeded.
func (s *Store) StatSidecar(hash, suffix string) (int64, bool) {
	if !entities.IsSHA256Hex(hash) {
		return 0, false
	}
	return statFile(SidecarPath(s.root, hash, suffix))
}

func statFile(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0, false
	}
	return info.Size(), true
}

// RemoveSidecars removes every derived file of a source: the readable rendering, the step record,
// the outline and the log (its rotation included), plus the rendering's old extension from a vault
// written before it was renamed. Deleting a source takes its whole story with it.
//
// `ext` is the source's OWN extension: the rendering is a plain ".md", which for a Markdown source is
// the object itself — and the object is the caller's business (it moves to the trash), never this.
func (s *Store) RemoveSidecars(hash, ext string) error {
	suffixes := append([]string{}, entities.SidecarSuffixes()...)
	suffixes = append(suffixes, entities.SidecarLog+".1")
	suffixes = append(suffixes, entities.SidecarRenderRenames...)
	for _, suffix := range suffixes {
		if suffix == strings.ToLower(ext) {
			continue
		}
		if err := os.Remove(SidecarPath(s.root, hash, suffix)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("objects: remove sidecar %s: %w", suffix, err)
		}
	}
	return nil
}

// RenameSidecars moves a source's derived files from one address to another: the step record, the
// outline, the log (its rotation included) and the rendering. It is how a source moves to the address
// it should have had — a URL source to its URL's hash — without re-deriving anything: the files are
// the same files, they are simply named after the address the source now has.
func (s *Store) RenameSidecars(oldHash, newHash string) error {
	if !entities.IsSHA256Hex(oldHash) || !entities.IsSHA256Hex(newHash) {
		return types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	suffixes := append([]string{}, entities.SidecarSuffixes()...)
	suffixes = append(suffixes, entities.SidecarLog+".1")
	for _, suffix := range suffixes {
		from := SidecarPath(s.root, oldHash, suffix)
		if _, err := os.Stat(from); err != nil {
			continue // not every source has every derived file
		}
		to := SidecarPath(s.root, newHash, suffix)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return fmt.Errorf("objects: create shard dir: %w", err)
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("objects: rename sidecar %s: %w", suffix, err)
		}
	}
	return nil
}

// PutRendered writes a DERIVED, readable rendering of an object next to it:
// <ab>/<cd>/<hash><suffix> (e.g. <hash>.md for a PDF's extracted text).
//
// It is deliberately not an object: not content-addressed, not deduplicated, and the store's scan
// ignores it (the name is not a hash plus one extension). It exists so a captured source stays
// readable in the vault without re-running an extractor — and it is rewritten from scratch, so a
// changed extractor shows up in it.
func (s *Store) PutRendered(hash, suffix string, content []byte) error {
	if !entities.IsSHA256Hex(hash) {
		return types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	dst := SidecarPath(s.root, hash, suffix)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("objects: create shard dir: %w", err)
	}
	return writeAtomic(dst, content)
}

// OpenRendered opens a derived rendering of an object (<hash><suffix>). A missing file is a normal
// state, not an error: not every source has a rendering, and a vault from before the step does not.
func (s *Store) OpenRendered(hash, suffix string) (io.ReadCloser, error) {
	if !entities.IsSHA256Hex(hash) {
		return nil, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	return os.Open(SidecarPath(s.root, hash, suffix))
}

// RemoveRendered drops a derived rendering again (a source that is deleted, or one that is no
// longer a PDF).
func (s *Store) RemoveRendered(hash, suffix string) error {
	err := os.Remove(SidecarPath(s.root, hash, suffix))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Scan walks the store and returns every object.
func (s *Store) Scan(_ context.Context) ([]entities.Object, error) {
	var out []entities.Object
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".tmp" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		obj, ok := parseObject(rel, path, d)
		if !ok {
			return nil
		}
		out = append(out, obj)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("objects: scan: %w", err)
	}
	return out, nil
}

// hasObjectSibling reports whether a source's own object sits next to this file — i.e. whether a
// plain ".md" here is a LEGACY rendering of a PDF/text source rather than a Markdown source itself.
// It exists for vaults written when the rendering was called <hash>.md; the current name
// (<hash>.capture.md) needs no such guess, because it cannot pass the object test at all.
func hasObjectSibling(dir, hash string) bool {
	for _, ext := range []string{".pdf", ".txt", ".markdown"} {
		if _, err := os.Stat(filepath.Join(dir, hash+ext)); err == nil {
			return true
		}
	}
	return false
}

func parseObject(rel, abs string, d fs.DirEntry) (entities.Object, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 {
		return entities.Object{}, false
	}
	name := parts[2]
	// The derived files sit next to the object and are NOT objects: their name is a hash plus a
	// suffix, not a hash plus one extension (<hash>.capture.md, <hash>.meta, <hash>.outline,
	// <hash>.summary.md, <hash>.log — the last of which would otherwise pass the hash check). The
	// rendering's first name is skipped here too. A plain ".md" from the rename in between is NOT
	// skipped by name — a Markdown source's object is a plain ".md" as well, so the sibling rule below
	// decides.
	if strings.Contains(name, entities.SidecarRenderLegacy) ||
		strings.HasSuffix(name, entities.SidecarMeta) || strings.HasSuffix(name, entities.SidecarOutline) ||
		strings.HasSuffix(name, entities.SidecarSummary) || strings.Contains(name, entities.SidecarLog) {
		return entities.Object{}, false
	}
	ext := filepath.Ext(name)
	hash := strings.TrimSuffix(name, ext)
	if !entities.IsSHA256Hex(hash) {
		return entities.Object{}, false
	}
	if parts[0] != hash[:2] || parts[1] != hash[2:4] {
		return entities.Object{}, false
	}
	// Only the legacy plain ".md" can collide with an object; the current rendering name cannot.
	if ext == ".md" && hasObjectSibling(filepath.Dir(abs), hash) {
		return entities.Object{}, false
	}
	info, err := d.Info()
	if err != nil {
		return entities.Object{}, false
	}
	return entities.Object{Hash: hash, Ext: strings.ToLower(ext), Path: filepath.ToSlash(rel), Bytes: info.Size()}, true
}

func relPath(hash, ext string) string {
	return filepath.Join(hash[:2], hash[2:4], hash+ext)
}

// writeAtomic rewrites one file in place: a UNIQUE temp file next to it, then a rename.
//
// The temp name must be unique. It used to be "<file>.tmp", and a URL snapshot is refreshed in place
// while its step record and its next fetch can all write the same address: two writers shared one temp
// file, and the first rename published the other one's half-written bytes.
func writeAtomic(dst string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("objects: create shard dir: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("objects: temp file: %w", err)
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("objects: write: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("objects: close: %w", err)
	}
	if err := os.Rename(name, dst); err != nil {
		return fmt.Errorf("objects: rename: %w", err)
	}
	return nil
}
