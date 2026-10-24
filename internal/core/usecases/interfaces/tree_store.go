package interfaces

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TreeStore persists a source's node tree as a derived sidecar. Trees are
// rebuildable and never the source of truth.
type TreeStore interface {
	Write(ctx context.Context, sourceHash string, nodes []entities.SourceNode) error
	Read(ctx context.Context, sourceHash string) ([]entities.SourceNode, error)
	// Remove deletes a source's tree sidecar (when the source is deleted).
	Remove(ctx context.Context, sourceHash string) error
}
