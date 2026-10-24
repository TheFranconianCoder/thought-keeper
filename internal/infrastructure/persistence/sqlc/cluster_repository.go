package sqlc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc/dal"
)

// ClusterRepository is the derived index of the association layer: the records in clusters/ mirrored
// into queryable rows. Membership is the CHILD row of a cluster, so SaveCluster replaces it
// wholesale — the same replace-shape rule a page save used, and the reason a narrow read must never
// be handed to a caller who would re-save it.
type ClusterRepository struct {
	db *sql.DB
	q  *dal.Queries
}

// NewClusterRepository wraps a database handle.
func NewClusterRepository(db *sql.DB) *ClusterRepository {
	return &ClusterRepository{db: db, q: dal.New(db)}
}

var _ interfaces.ClusterRepository = (*ClusterRepository)(nil)

// SaveCluster mirrors one record and replaces its membership.
func (r *ClusterRepository) SaveCluster(ctx context.Context, c entities.Cluster) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := r.q.WithTx(tx)
	created := c.Created
	if created.IsZero() {
		created = c.Updated
	}
	if err := q.UpsertCluster(ctx, dal.UpsertClusterParams{
		ID:        c.ID,
		Signature: c.Signature,
		CreatedAt: created.UnixMilli(),
		UpdatedAt: c.Updated.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("save cluster: %w", err)
	}
	if err := q.DeleteClusterAtoms(ctx, c.ID); err != nil {
		return fmt.Errorf("clear cluster membership: %w", err)
	}
	for _, atom := range c.Atoms {
		if err := q.InsertClusterAtom(ctx, dal.InsertClusterAtomParams{ClusterID: c.ID, AtomID: atom}); err != nil {
			return fmt.Errorf("add cluster member: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteCluster removes a cluster (its membership cascades).
func (r *ClusterRepository) DeleteCluster(ctx context.Context, id ulid.ULID) error {
	if err := r.q.DeleteCluster(ctx, id); err != nil {
		return fmt.Errorf("delete cluster: %w", err)
	}
	return nil
}

// GetCluster loads one cluster with its membership.
func (r *ClusterRepository) GetCluster(ctx context.Context, id ulid.ULID) (entities.Cluster, error) {
	row, err := r.q.GetCluster(ctx, id)
	if err != nil {
		return entities.Cluster{}, err
	}
	atoms, err := r.q.ListClusterAtoms(ctx, id)
	if err != nil {
		return entities.Cluster{}, err
	}
	return toCluster(row, atoms), nil
}

// ListClusters returns clusters with their membership.
func (r *ClusterRepository) ListClusters(ctx context.Context, limit, offset int) ([]entities.Cluster, error) {
	rows, err := r.q.ListClusters(ctx, dal.ListClustersParams{Limit: int64(limit), Offset: int64(offset)})
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	if len(rows) == 0 {
		return []entities.Cluster{}, nil
	}
	// Membership in ONE read, for the PAGE's clusters and no others, grouped in the same per-cluster
	// atom order the per-cluster query answers in. The per-row ListClusterAtoms loop was an N+1 (13k
	// queries per list read); loading the whole membership table instead would be the same mistake
	// the Sources list made, one page size up.
	ids := make([]ulid.ULID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	memberships, err := r.q.ClusterMembershipsForClusters(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list clusters: membership: %w", err)
	}
	byCluster := make(map[ulid.ULID][]ulid.ULID, len(rows))
	for _, m := range memberships {
		byCluster[m.ClusterID] = append(byCluster[m.ClusterID], m.AtomID)
	}
	out := make([]entities.Cluster, 0, len(rows))
	for _, row := range rows {
		out = append(out, toCluster(row, byCluster[row.ID]))
	}
	return out, nil
}

// ClusterIDsOfAtom returns the clusters a claim belongs to.
func (r *ClusterRepository) ClusterIDsOfAtom(ctx context.Context, atomID ulid.ULID) ([]ulid.ULID, error) {
	ids, err := r.q.ListClusterIDsForAtom(ctx, atomID)
	if err != nil {
		return nil, fmt.Errorf("clusters of atom: %w", err)
	}
	return ids, nil
}

// AtomsOfCluster returns a cluster's member atoms.
func (r *ClusterRepository) AtomsOfCluster(ctx context.Context, clusterID ulid.ULID) ([]ulid.ULID, error) {
	atoms, err := r.q.ListClusterAtoms(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("atoms of cluster: %w", err)
	}
	return atoms, nil
}

// MembershipsForAtoms returns the cluster memberships of exactly these claims, atom-ordered.
func (r *ClusterRepository) MembershipsForAtoms(ctx context.Context, ids []ulid.ULID) ([]entities.ClusterMembership, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.q.ClusterMembershipsForAtoms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("memberships for atoms: %w", err)
	}
	out := make([]entities.ClusterMembership, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.ClusterMembership{ClusterID: row.ClusterID, AtomID: row.AtomID})
	}
	return out, nil
}

// CountClusters is the status figure.
func (r *ClusterRepository) CountClusters(ctx context.Context) (int64, error) {
	n, err := r.q.CountClusters(ctx)
	if err != nil {
		return 0, fmt.Errorf("count clusters: %w", err)
	}
	return n, nil
}

// CountUnclusteredAtoms is the backlog figure: claims with no membership yet.
func (r *ClusterRepository) CountUnclusteredAtoms(ctx context.Context) (int64, error) {
	n, err := r.q.CountUnclusteredAtoms(ctx)
	if err != nil {
		return 0, fmt.Errorf("count unclustered atoms: %w", err)
	}
	return n, nil
}

func toCluster(row dal.Cluster, atoms []ulid.ULID) entities.Cluster {
	return entities.Cluster{
		ID: row.ID, Signature: row.Signature,
		Created: time.UnixMilli(row.CreatedAt).UTC(),
		Updated: time.UnixMilli(row.UpdatedAt).UTC(),
		Atoms:   atoms,
	}
}
