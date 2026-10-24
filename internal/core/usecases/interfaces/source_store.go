package interfaces

import (
	"context"
	"io"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// SourceStore is the content-addressed raw store. Objects are named by their
// SHA-256, sharded into subdirectories, write-once and deduplicated.
type SourceStore interface {
	// PutRendered writes a derived, readable rendering of an object next to it
	// (sources/<ab>/<cd>/<hash>.md). Derived, not an object — never deduplicated, never
	// scanned as a source.
	PutRendered(hash, suffix string, content []byte) error
	// OpenRendered reads such a rendering back.
	OpenRendered(hash, suffix string) (io.ReadCloser, error)
	// RemoveRendered drops one derived rendering (a renamed one, or a source that is no longer a PDF).
	RemoveRendered(hash, suffix string) error
	// RemoveSidecars removes every derived file of a source (rendering, step record, outline, log).
	// Deleting a source deletes its whole story: nothing about it may linger. `ext` is the source's
	// own extension — the rendering is a plain ".md", which for a Markdown source IS the object.
	RemoveSidecars(hash, ext string) error
	// StatObject returns the object's size and whether it exists (the source itself).
	StatObject(hash, ext string) (int64, bool)
	// StatSidecar returns a derived file's size and whether it exists (the outline, the rendering,
	// …) — what the consistency job checks the step records against, and what the detail view shows.
	StatSidecar(hash, suffix string) (int64, bool)
	// Put hashes r and stores it under the given extension (e.g. ".md"). Storing
	// existing bytes is a no-op that returns the existing object.
	Put(ctx context.Context, ext string, r io.Reader) (entities.Object, error)
	// PutAt writes bytes at a FIXED address, replacing what is there. It is for the two MUTABLE
	// sources: a URL snapshot and a wiki page representation. Their content is a moving target, so
	// the address is assigned once and the bytes are refreshed in place. Everything else is
	// content-addressed and write-once (Put).
	PutAt(hash, ext string, content []byte) (entities.Object, error)
	// Open returns the bytes of the object with the given hash and extension.
	Open(hash, ext string) (io.ReadCloser, error)
	// Scan walks the store and returns every object.
	Scan(ctx context.Context) ([]entities.Object, error)
	// Remove takes an object out of the store (it is moved to the trash, never
	// hard-deleted), so a reindex cannot resurrect a deleted source.
	Remove(ctx context.Context, hash, ext string) error
	// RenameSidecars moves a source's derived files (rendering, step record, outline, log) from one
	// address to another. It is how a source moves to the address it should have had without being
	// derived again: the files are the same files, named after the address the source now has.
	RenameSidecars(oldHash, newHash string) error
}
