package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// VocabularyReporter reads the observed catalogs against the decided vocabularies.
type VocabularyReporter interface {
	Execute(ctx context.Context) ([]partialentities.VocabularyHealth, error)
}

// VocabularyScheduler queues a curation run for one namespace.
type VocabularyScheduler interface {
	ScheduleVocabularyCurate(ns entities.VocabularyNamespace) (string, error)
	// ScheduleVocabularyRebranch re-derives the topic labels of the claims.
	ScheduleVocabularyRebranch() (string, error)
}

// VocabularyHandler serves GET /api/v1/vocabulary: the health of the name vocabularies — how many
// names there are, how much of the knowledge the head carries, what is mechanically wrong, and what the
// curation has not seen yet.
type VocabularyHandler struct {
	Report  VocabularyReporter
	Consume VocabularyScheduler
}

// Get serves the report.
func (h VocabularyHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Report == nil {
		payloads.WriteError(w, types.New("not_implemented", "vocabulary is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.Report.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.VocabularyFromPartials(res))
}

// Curate serves POST /api/v1/vocabulary/curate: one namespace, one run. The run asks the
// model what the names should become (with the evidence each namespace needs) and applies the answer as
// a rename — the agent owns the vocabulary, and nothing about it is manual.
func (h VocabularyHandler) Curate(w http.ResponseWriter, r *http.Request) {
	if h.Consume == nil {
		payloads.WriteError(w, types.New("not_implemented", "vocabulary curation is not configured", types.ErrNotImplemented))
		return
	}
	// No body means the whole unit (baseline, then claim topics → claim topics): the curation is
	// ONE action, and a caller that wants only one namespace says so. An empty body is therefore not
	// an error — it is the normal request.
	var req payloads.VocabularyCurateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	runID, err := h.Consume.ScheduleVocabularyCurate(entities.VocabularyNamespace(req.Namespace))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: "running"})
}

// Rebranch serves POST /api/v1/vocabulary/retopic: the TOPIC labels of the claims are re-derived from
// the claims themselves — the repair for a topic vocabulary that was collapsed into umbrellas. It
// rewrites no claim and touches no page.
func (h VocabularyHandler) Rebranch(w http.ResponseWriter, r *http.Request) {
	if h.Consume == nil {
		payloads.WriteError(w, types.New("not_implemented", "re-topic is not configured", types.ErrNotImplemented))
		return
	}
	runID, err := h.Consume.ScheduleVocabularyRebranch()
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: "running"})
}
