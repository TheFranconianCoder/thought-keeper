package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ClusterRepository is the derived index of the association layer: the records in `clusters/`
// mirrored into queryable rows. Membership is the child row of a cluster, and SaveCluster is
// authoritative for it — it deletes and re-inserts the cluster's membership (the same replace-shape
// rule the old page save followed), so a caller that loaded a cluster narrowly must never save it.
//
// It answers exactly the two reads the claim-centric UI and Ask need: "which cluster(s) hold this
// claim?" and "which claims share that cluster?" — a claim's related set, no pages involved.
type ClusterRepository interface {
	// SaveCluster mirrors one record and replaces its membership rows.
	SaveCluster(ctx context.Context, c entities.Cluster) error
	// DeleteCluster removes a cluster and (by cascade) its membership rows.
	DeleteCluster(ctx context.Context, id ulid.ULID) error
	// GetCluster loads one cluster with its membership.
	GetCluster(ctx context.Context, id ulid.ULID) (entities.Cluster, error)
	// ListClusters returns every cluster id with its membership, sharded-order stable.
	ListClusters(ctx context.Context, limit, offset int) ([]entities.Cluster, error)
	// ClusterIDsOfAtom returns the clusters a claim belongs to (a claim may sit in several).
	ClusterIDsOfAtom(ctx context.Context, atomID ulid.ULID) ([]ulid.ULID, error)
	// AtomsOfCluster returns the cluster's member atoms, id-ordered.
	AtomsOfCluster(ctx context.Context, clusterID ulid.ULID) ([]ulid.ULID, error)
	// MembershipsForAtoms returns the memberships of exactly these claims — what a list row asks to
	// show "travels in N clusters" for the page it returns.
	MembershipsForAtoms(ctx context.Context, ids []ulid.ULID) ([]entities.ClusterMembership, error)
	// CountClusters is the status figure: how many clusters exist.
	CountClusters(ctx context.Context) (int64, error)
	// CountUnclusteredAtoms is the backlog figure: claims with no membership yet — the one number
	// the pipeline pump and the status page read to decide whether step 4 has work.
	CountUnclusteredAtoms(ctx context.Context) (int64, error)
}
