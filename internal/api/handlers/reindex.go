package handlers

import (
	"context"
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// ReindexService rebuilds the source index from the store.
type ReindexService interface {
	Execute(ctx context.Context) (usecases.ReindexResult, error)
}

// ReindexHandler serves POST /api/v1/reindex.
type ReindexHandler struct {
	Reindex ReindexService
	Mark    MaintenanceMarker
}

// Post runs a reindex synchronously (it only walks the store).
func (h ReindexHandler) Post(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("reindex", "")()
	}
	res, err := h.Reindex.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.ReindexResult{
		Scanned:          res.Scanned,
		Indexed:          res.Indexed,
		Added:            res.Added,
		Removed:          res.Removed,
		AtomsRemoved:     res.AtomsRemoved,
		AtomsOrphaned:    res.AtomsOrphaned,
		ProvenancePruned: res.ProvenancePruned,
		Clusters:         res.Clusters,
		ClustersRemoved:  res.ClustersRemoved,
		Atoms:            res.Atoms,
		Steps:            res.Steps,
		PartsPruned:      res.PartsPruned,
		Skipped:          res.Skipped,
		Orphans:          int(res.Orphans),
	})
}
