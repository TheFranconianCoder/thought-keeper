package interfaces

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// SourceTagStore reads and writes the HUMAN tags on sources. The source's <hash>.meta record is the
// truth; the index mirrors it. The vocabulary uses SourceTags to rename labels (a rename is a record
// write, never a derivation), and the API uses SetSourceTags for the editor.
type SourceTagStore interface {
	SourceTags(ctx context.Context) (map[string]entities.SourceTagSet, error)
	SetSourceTags(ctx context.Context, hash string, tags []string) error
	SetSourceAutoTags(ctx context.Context, hash string, tags []string, at time.Time) error
}

// SourceTagReader observes the source-tag catalog for the vocabulary report: each label and how many
// sources carry it. The index answers it; the .meta records remain the truth a reindex reads.
type SourceTagReader interface {
	SourceTagCounts(ctx context.Context) (map[string]int, error)
}
