package usecases

import (
	"context"
	"fmt"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// maxSourceTags bounds the labels one source may carry. A tag is a coarse shelf, not prose; a hundred
// labels on one source would be a vocabulary of its own, and the editor cannot read them.
const maxSourceTags = 32

// SetSourceTags replaces a source's HUMAN tags. It is the same kind of label a wiki page carries: the
// reader files and filters by it. The source's <hash>.meta record is the truth; the index mirrors it.
// This is a metadata write, not a derivation: no claim, tree or step is touched.
type SetSourceTags struct {
	Index     interfaces.IndexRepository
	Tags      interfaces.SourceTagStore
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
}

// Execute normalizes and stores the labels, then returns the source as the API shows it.
func (uc SetSourceTags) Execute(ctx context.Context, hash string, tags []string) (partialentities.PartialSource, error) {
	if !entities.IsSHA256Hex(hash) {
		return partialentities.PartialSource{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	if _, err := uc.Index.GetSourceByHash(ctx, hash); err != nil {
		return partialentities.PartialSource{}, err
	}
	normalized := normalizeSourceTags(tags)
	if len(normalized) > maxSourceTags {
		return partialentities.PartialSource{}, types.New("invalid_tags",
			fmt.Sprintf("a source can carry at most %d tags", maxSourceTags), types.ErrInvalidTags)
	}
	if uc.Tags == nil {
		return partialentities.PartialSource{}, fmt.Errorf("source tags: no store configured")
	}
	if err := uc.Tags.SetSourceTags(ctx, hash, normalized); err != nil {
		return partialentities.PartialSource{}, err
	}
	if uc.SourceLog != nil && uc.Clock != nil {
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "tags", Level: "info", Message: "tags changed",
			Fields: map[string]any{"tags": len(normalized)},
		})
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	return partialentities.PartialFromSource(src), nil
}

// normalizeSourceTags applies the same shape as a wiki tag (lowercase, hyphenated) and drops empty and
// duplicate names. The order is the reader's.
func normalizeSourceTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, raw := range tags {
		tag := coremarkdown.NormalizeTag(raw)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}
