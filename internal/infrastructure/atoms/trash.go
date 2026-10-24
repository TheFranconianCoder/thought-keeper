package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// trashDir is the vault's local bin for atoms: `<vault>/.trash/atoms/` — the same place the object
// store uses for its own removals, one level up from this store's root.
func (s *Store) trashDir() string {
	return filepath.Join(filepath.Dir(s.root), ".trash", "atoms")
}

// Trash moves an atom's file out of the store into the trash. Trash, not delete: a claim that turned
// out to be a copy of another one is still recoverable, and the file was the only place its content
// lived.
func (s *Store) Trash(_ context.Context, a entities.Atom) error {
	if a.Path == "" {
		return nil
	}
	src := filepath.Join(s.root, filepath.FromSlash(a.Path))
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("atoms: stat: %w", err)
	}
	if err := os.MkdirAll(s.trashDir(), 0o755); err != nil {
		return fmt.Errorf("atoms: trash dir: %w", err)
	}
	dst := filepath.Join(s.trashDir(), filepath.Base(src))
	_ = os.Remove(dst)
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("atoms: trash: %w", err)
	}
	return nil
}
