package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// StepTracker records the state of a source's DOCUMENT steps (capture, outline, distill). The
// four steps are the pipeline's contract with the reader, so each of them reports where it stands
// instead of the state being inferred from whatever happens to be in the index.
//
// A nil tracker is fine: the pipeline then runs exactly as before, it just has no lights. The
// implementation writes the vault's <hash>.meta file (the truth) and mirrors it into the index.
type StepTracker interface {
	// Record writes one step transition for one source.
	Record(ctx context.Context, hash string, step entities.Step, st entities.StepStatus) error
	// Steps returns what a source's document steps are recorded as (empty when never recorded).
	Steps(hash string) (entities.StepSet, error)
	// SetParts records a run's part outcomes (which text-bearing node is finished, and why not).
	SetParts(hash string, parts map[string]entities.PartRecord) error
	// Records returns every recorded source, keyed by hash: the step lights, the title and the part
	// records. This is how a reindex rebuilds the index's mirror from the vault alone.
	Records() (map[string]entities.SourceRecord, error)
	// Reset clears a source's derivation steps after a reset: the derivation is gone, so nothing
	// that was recorded about it is true any more. Capture survives — the source itself does.
	Reset(hash string) error
	// Remove drops the record with the source.
	Remove(hash string) error
	// SetTitle records the document's title in the record and mirrors it into the index. Step 1
	// determines it; the outline may refine it. The file is the truth, so a lost title comes back
	// with a reindex.
	SetTitle(hash, title string) error
	// SetIdentity records what a source IS — its original kind, the URL it was fetched from, whether
	// it is a collected link, its detected language and the name it came in under. The object's file
	// name cannot carry the kind any more (a URL snapshot and a Markdown source are both <hash>.md),
	// so this is what keeps a URL recognizable: a reindex reads it back instead of guessing from an
	// extension.
	SetIdentity(hash string, id entities.SourceIdentity) error
	// Identity returns what the source's RECORD says the source is. The consistency job asks it to
	// find a record that lost its identity (a truncated write): the index still knows what the source
	// was, and the record is the vault's truth a reindex would otherwise read as "Markdown".
	Identity(hash string) (entities.SourceIdentity, error)
	// SetLanguage records the source's detected language in the vault record and index.
	SetLanguage(hash, language string) error
	// SetOrigin marks the source as created by a wiki page (safe removal tracking).
	SetOrigin(hash, origin, wikiPage string) error
	// Release removes the ownership of a sub-source: the human added the URL themselves, and a manual
	// source must not disappear with the page or sitemap that had claimed it first.
	Release(hash string) error
	// Rehash moves a source's record to another address. The record carries the hash it belongs to, so
	// a source that moves (a URL source onto its URL's address) would otherwise keep writing its steps
	// to the address it no longer has.
	Rehash(oldHash, newHash string) error
}
