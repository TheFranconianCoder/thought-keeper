package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// TreeService returns one source's outline as the tree view shows it. A handler holds this value; nil
// means the feature is not wired.
type TreeService func(ctx context.Context, hash string) ([]partialentities.PartialNode, error)

// Tree returns the outline of one source.
func (uc Trees) Tree(ctx context.Context, hash string) ([]partialentities.PartialNode, error) {
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

// NodeService returns one node of an outline, with the text it cites. A handler holds this value; nil
// means the feature is not wired.
type NodeService func(ctx context.Context, hash, nodeID string) (partialentities.PartialNode, error)

// Node returns one node of the outline.
func (uc Trees) Node(ctx context.Context, hash, nodeID string) (partialentities.PartialNode, error) {
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
