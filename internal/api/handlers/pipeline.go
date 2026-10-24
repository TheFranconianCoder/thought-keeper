package handlers

import (
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// PipelineProvider reports the global pipeline status: which of the four strictly separated steps
// is running. The title bar shows it, so a user can see whether documents are being derived, the
// cluster pass is running, or nothing is running at all.
type PipelineProvider interface {
	PipelineStatus() payloads.Pipeline
}

// PipelineHandler serves GET /api/v1/pipeline.
type PipelineHandler struct {
	Pipeline PipelineProvider
}

// Get answers the current status. It is a read: Ask must always be possible.
func (h PipelineHandler) Get(w http.ResponseWriter, r *http.Request) {
	payloads.WriteJSON(w, http.StatusOK, h.Pipeline.PipelineStatus())
}

// ClusterQueue queues the CLUSTER step by hand. It is the one step that is steered globally rather
// than per source: it runs over the whole vault and only while no document step runs, so the request
// is queued and the gate decides when it starts.
type ClusterQueue interface {
	ScheduleCluster() (string, error)
}

// Reconciler runs the consistency job by hand.
type Reconciler interface {
	ReconcileNow() (usecases.ReconcileResult, error)
}

// ReconcileHandler serves POST /api/v1/reconcile: the same pass the periodic worker runs — check
// that what the step records say is on disk, invalidate what is not, and ask the cluster step for a
// pass if there is something for it to do.
type ReconcileHandler struct {
	Runs Reconciler
	Mark MaintenanceMarker
}

// Post runs one consistency pass.
func (h ReconcileHandler) Post(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("reconcile", "")()
	}
	res, err := h.Runs.ReconcileNow()
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.ReconcileResult{
		Checked: res.Checked, Invalidated: res.Invalidated, Broken: res.Broken, Languages: res.Languages,
		Identities: res.Identities, Ownerships: res.Ownerships, Summaries: len(res.MissingSummaries),
	})
}

// ClusterHandler serves POST /api/v1/cluster.
type ClusterHandler struct {
	Runs ClusterQueue
}

// Post queues a cluster pass. 202 carries the run id; an empty run id means there was nothing to do
// (no claim without a cluster) — not an error.
func (h ClusterHandler) Post(w http.ResponseWriter, r *http.Request) {
	runID, err := h.Runs.ScheduleCluster()
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if runID == "" {
		payloads.WriteJSON(w, http.StatusOK, payloads.ClusterResult{Queued: false})
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.ClusterResult{Queued: true, RunID: runID})
}
