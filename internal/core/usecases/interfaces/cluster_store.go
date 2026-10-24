package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ClusterStore persists cluster records as the vault's `clusters/` tree: one JSON `.meta` per
// cluster, addressed by the cluster's ULID and sharded by its tail exactly like atoms and (the old)
// pages. It is the record of the association step; the page Markdown it used to sit next to is gone.
//
// Records are a cache — the claims are the truth — so the store has no Read/Write of a rendering, no
// generated index and no log: a full re-derivation replaces the whole directory.
type ClusterStore interface {
	// Path is the vault-relative, sharded record path of a cluster.
	Path(id ulid.ULID) string
	// WriteMeta stores a cluster record (creating its shard folders as needed).
	WriteMeta(ctx context.Context, c entities.Cluster) error
	// ReadMeta loads one cluster record.
	ReadMeta(ctx context.Context, id ulid.ULID) (entities.Cluster, error)
	// ScanMetas returns every cluster record in the vault (for reindex and re-derivation).
	ScanMetas(ctx context.Context) ([]entities.Cluster, error)
	// RemoveMeta deletes one cluster record.
	RemoveMeta(ctx context.Context, id ulid.ULID) error
}
