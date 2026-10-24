package interfaces

import "context"

// Maintenance repairs referential damage in the index (orphaned child rows whose parent is
// gone), so counting and resolving agree again.
type Maintenance interface {
	PruneOrphans(ctx context.Context) (int64, error)
}
