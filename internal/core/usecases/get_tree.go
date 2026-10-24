package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// GetTree returns a source's node outline (without node text).
type GetTree struct {
	Index interfaces.IndexRepository
}

// Execute returns the nodes in document order, text stripped.
func (uc GetTree) Execute(ctx context.Context, hash string) ([]partialentities.PartialNode, error) {
	if !entities.IsSHA256Hex(hash) {
		return nil, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	nodes, err := uc.Index.ListSourceNodes(ctx, src.ID)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialNode, 0, len(nodes))
	for _, n := range nodes {
		p := partialentities.PartialFromNode(n)
		p.Text = ""
		out = append(out, p)
	}
	return out, nil
}

// GetNode returns one node from the tree sidecar, including its text slice — resolved from the node's
// REFERENCE, because the outline does not carry the text.
type GetNode struct {
	Trees interfaces.TreeStore
	Index interfaces.IndexRepository
	Store interfaces.SourceStore
}

// Execute returns the node with the given id for a source.
func (uc GetNode) Execute(ctx context.Context, hash, nodeID string) (partialentities.PartialNode, error) {
	if !entities.IsSHA256Hex(hash) {
		return partialentities.PartialNode{}, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	nodes, err := uc.Trees.Read(ctx, hash)
	if err != nil {
		return partialentities.PartialNode{}, err
	}
	for _, n := range nodes {
		if n.NodeID != nodeID {
			continue
		}
		resolved, err := nodeTexts(ctx, uc.Index, uc.Store, hash, []entities.SourceNode{n})
		if err != nil {
			return partialentities.PartialNode{}, err
		}
		return partialentities.PartialFromNode(resolved[0]), nil
	}
	return partialentities.PartialNode{}, types.New("not_found", "no such node in this source", types.ErrNotFound)
}
