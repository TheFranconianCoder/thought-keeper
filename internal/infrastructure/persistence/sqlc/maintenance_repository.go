package sqlc

import (
	"context"
	"database/sql"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc/dal"
)

// MaintenanceRepository repairs referential damage in the index.
type MaintenanceRepository struct {
	db *sql.DB
	q  *dal.Queries
}

// NewMaintenanceRepository returns a maintenance repository over db.
func NewMaintenanceRepository(db *sql.DB) *MaintenanceRepository {
	return &MaintenanceRepository{db: db, q: dal.New(db)}
}

var _ interfaces.Maintenance = (*MaintenanceRepository)(nil)

// PruneOrphans deletes child rows whose parent is gone (atom_sources without an atom,
// cluster_atoms without a cluster, and so on) and returns how many it removed. Foreign keys keep
// this from happening; the prune is the repair for an index that drifted or was written
// while foreign keys were off, and it makes counts agree with what can be resolved again.
func (r *MaintenanceRepository) PruneOrphans(ctx context.Context) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	q := r.q.WithTx(tx)

	var total int64
	for _, del := range []func(context.Context) (int64, error){
		q.DeleteOrphanAtomSources,
		q.DeleteOrphanClusterAtoms,
		q.DeleteOrphanSourceNodes,
	} {
		n, err := del(ctx)
		if err != nil {
			return total, err
		}
		total += n
	}
	if err := tx.Commit(); err != nil {
		return total, err
	}
	return total, nil
}
