package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// AtomStore persists claims as Markdown files addressed by their ID. There is no usage record any
// more: its only purpose was to answer "which pages must be marked stale?", and with the page layer
// gone the association layer is a deterministic cache that a re-run rebuilds.
type AtomStore interface {
	// Write stores the atom file at its canonical path and returns the persisted atom (with the final
	// topic and path).
	Write(ctx context.Context, a entities.Atom) (entities.Atom, error)
	// Read loads an atom by its id.
	Read(ctx context.Context, id ulid.ULID) (entities.Atom, error)
	// Remove deletes an atom's Markdown file (when the atom is deleted).
	Remove(ctx context.Context, a entities.Atom) error
	// Trash moves an atom's file into the vault's .trash/atoms/. Trash, not delete: a claim that
	// turned out to be a copy of another one is still recoverable.
	Trash(ctx context.Context, a entities.Atom) error
	// Scan walks the atom store and returns every atom (for a rebuild).
	Scan(ctx context.Context) ([]entities.Atom, error)
	// DetectLegacyFolders reports the topic folders a vault written before the ID layout still holds
	// — a directory under atoms/ that carries a `.md` file DIRECTLY — with how many claims and how
	// many generated indexes they hold. Read-only.
	DetectLegacyFolders() (folders []string, claims, indexes int, err error)
	// Reorganize moves every atom out of those folders to its canonical path, removes the generated
	// topic indexes (they catalog a folder that no longer exists) and removes the folders that empty
	// out. It returns the atoms it MOVED — the index has to learn their new paths, or the next write
	// recreates the folder it just removed — plus how many folders it removed, how many indexes it
	// deleted, how many files it could not read, and the folders that still hold something.
	Reorganize(ctx context.Context) (moved []entities.Atom, folders, indexes, skipped int, kept []string, err error)
	// RemoveStaleIndex deletes a generated atoms/index.md left by an older build: the topic catalog is
	// not written any more, because the index is SQLite's job.
	RemoveStaleIndex() (bool, error)
}
