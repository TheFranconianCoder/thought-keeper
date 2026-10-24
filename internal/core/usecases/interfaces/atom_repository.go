package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// AtomRepository is the derived index for atoms.
type AtomRepository interface {
	SaveAtom(ctx context.Context, a entities.Atom) error
	GetAtom(ctx context.Context, id ulid.ULID) (entities.Atom, error)
	ListAtoms(ctx context.Context, topic string, limit, offset int) ([]entities.Atom, error)
	// ListAtomsBySource lists the atoms that cite a source.
	ListAtomsBySource(ctx context.Context, sourceHash string) ([]entities.Atom, error)
	// ListOldestAtoms returns the OLDEST claims in (updated_at, id) order, up to limit — id, topic,
	// claim and body only. It is the queue a time-bounded pass reads: no provenance, no links, no
	// whole-table hydration. Whatever it changes is re-read whole before the write, because a SAVE
	// is authoritative for provenance and links.
	ListOldestAtoms(ctx context.Context, limit int) ([]entities.Atom, error)
	// SupersedeAtomsBySource marks every atom citing that source as superseded —
	// used when a URL's content changes and its old atoms are stale.
	SupersedeAtomsBySource(ctx context.Context, sourceHash string) (int64, error)
	// DeleteAtom removes an atom from the index.
	DeleteAtom(ctx context.Context, id ulid.ULID) error
	// CountAtomSources returns how many sources an atom cites.
	CountAtomSources(ctx context.Context, id ulid.ULID) (int, error)
	// NodeRefsBySource returns, per atom of a source, the tree nodes it cites.
	NodeRefsBySource(ctx context.Context, hash string) (map[ulid.ULID][]string, error)
	ListClaimTopics(ctx context.Context) ([]entities.TopicUse, error)
	// StreamAtomTexts walks EVERY claim's text in id order, handing one claim to fn at a time and
	// keeping none: the association pass folds its statistics from the stream instead of
	// materialising the corpus. fn must not retain the atom it receives.
	StreamAtomTexts(ctx context.Context, fn func(entities.Atom) error) error
	// StreamClaimIdentities walks every claim's IDENTITY in id order — id, topic, claim, body, type,
	// status and provenance — handing one claim to fn at a time. It is what the duplicate check
	// needs; reading the atom files for it decoded the whole vault to answer a 200-byte question.
	StreamClaimIdentities(ctx context.Context, fn func(entities.Atom) error) error
	// AtomTextsForIDs returns the text of exactly these claims: the signature of a touched record
	// is recomputed from its members, and touching a record per pass is the common case.
	AtomTextsForIDs(ctx context.Context, ids []ulid.ULID) ([]entities.Atom, error)
	// AtomCount is how many claims the index holds: the total of a progress line, without reading one.
	AtomCount(ctx context.Context) (int, error)
	// SourcesForAtoms returns the provenance of exactly these claims, keyed by id. The node a claim
	// cites is the strongest signal the association step has.
	SourcesForAtoms(ctx context.Context, ids []ulid.ULID) (map[ulid.ULID][]entities.AtomSource, error)
	// AtomIDs returns the id of every claim: the existence set a consistency check compares the
	// cluster membership against.
	AtomIDs(ctx context.Context) ([]ulid.ULID, error)
	// AtomAddresses returns the id and the recorded file path of every claim — what a stale index row
	// needs in order to remove the file it points at.
	AtomAddresses(ctx context.Context) ([]entities.Atom, error)
}
