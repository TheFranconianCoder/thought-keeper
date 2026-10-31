package interfaces

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/interests"
)

// ShelfStore is the reader's own marks on a source: the favorite (a shelf) and the read-later
// queue. The source's <hash>.meta record is the truth; the index mirror is what the windowed list
// filters and sorts by.
type ShelfStore interface {
	// SetFavorite writes or clears the star. A zero time clears it.
	SetFavorite(ctx context.Context, hash string, at time.Time) error
	// SetReadLater writes one queue state: the entry time (zero = never queued), the done time
	// (zero = still open), whether the reader put it there by hand, and the interests that did.
	SetReadLater(ctx context.Context, hash string, at, doneAt time.Time, manual bool, via []string) error
	// QueueFromInterests is the AUTOMATIC write: it appends the interests that matched to an entry
	// that is absent or open, never re-opens a finished one, and reports whether the record changed.
	// It is one read-modify-write under the source's lock, because a manual queue and a distill can
	// finish at the same moment.
	QueueFromInterests(ctx context.Context, hash string, via []string, at time.Time) (bool, error)
}

// ShelfCounter answers the vault-wide numbers the Sources head and the Start card show. They are
// counts of the WHOLE queue, never of the page a window happens to have loaded.
type ShelfCounter interface {
	CountReadLater(ctx context.Context) (int, error)
	CountReadLaterFromInterests(ctx context.Context) (int, error)
	CountReadLaterVia(ctx context.Context, slug string) (int, error)
	CountFavorites(ctx context.Context) (int, error)
}

// ShelfMirror is the index side of the shelf, used by the reindex to restore the columns from the
// records (the vault is the truth; the index is the cache).
type ShelfMirror interface {
	SetSourceFavorite(ctx context.Context, hash string, at time.Time) error
	SetSourceReadLater(ctx context.Context, hash string, at, doneAt time.Time, manual bool, via []string) error
}

// InterestStore persists the reader's rules in the vault: one Markdown file per interest under
// interests/. It is the truth for the rule (its terms, its pause state, its rejections), because a
// rule is a decision and not an observation the index could rebuild.
type InterestStore interface {
	List(ctx context.Context) ([]entities.Interest, error)
	Get(ctx context.Context, slug string) (entities.Interest, error)
	Save(ctx context.Context, in entities.Interest) error
	Delete(ctx context.Context, slug string) error
	// SetRejected records or clears one source's correction under the rule's own lock.
	SetRejected(ctx context.Context, slug, hash string, rejected bool) (entities.Interest, error)
}

// InterestCandidate is one source the preview may match: what the source IS and the per-term signals
// the lexical layer found. The use case decides the band; the adapter only reports evidence.
type InterestCandidate struct {
	Hash    string
	Title   string
	Kind    entities.SourceKind
	Signals interests.Signals
}

// InterestSignalReader is the preview's read over the WHOLE vault. It is only called for an
// explicit rule change or its calibration preview — never on the ingest path, which matches one
// source at a time in memory.
type InterestSignalReader interface {
	Candidates(ctx context.Context, terms []string) ([]InterestCandidate, error)
}
