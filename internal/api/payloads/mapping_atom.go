package payloads

import (
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// AtomFromPartial maps an atom partial to its wire shape.
func AtomFromPartial(p partialentities.PartialAtom) Atom {
	srcs := make([]AtomSource, 0, len(p.Sources))
	for _, s := range p.Sources {
		srcs = append(srcs, AtomSource{Hash: s.Hash, Title: s.Title, Node: s.NodeID, OCR: s.OCR})
	}
	clusters := make([]string, 0, len(p.Clusters))
	for _, id := range p.Clusters {
		clusters = append(clusters, id.String())
	}
	return Atom{
		ID:           p.ID.String(),
		Topic:        p.Topic,
		Path:         p.Path,
		Claim:        p.Claim,
		Body:         p.Body,
		Type:         p.Type,
		Status:       p.Status,
		Sources:      srcs,
		SourceCount:  p.SourceCount,
		Clusters:     clusters,
		ClusterCount: len(clusters),
		CreatedAt:    p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// AtomsFromPartials maps a slice of atom partials.
func AtomsFromPartials(ps []partialentities.PartialAtom) []Atom {
	out := make([]Atom, 0, len(ps))
	for _, p := range ps {
		out = append(out, AtomFromPartial(p))
	}
	return out
}

// TopicUsesFromPartials maps claim-topic partials.
func TopicUsesFromPartials(ps []partialentities.PartialBranch) []TopicUse {
	out := make([]TopicUse, 0, len(ps))
	for _, p := range ps {
		out = append(out, TopicUse{Topic: p.Topic, Count: p.Count})
	}
	return out
}

// RunFromPartial maps a run partial to its wire shape.
func RunFromPartial(p partialentities.PartialRun) Run {
	r := Run{
		ID:               p.ID,
		Op:               p.Op,
		Model:            p.Model,
		TargetID:         p.TargetID,
		Status:           p.Status,
		PromptTokens:     p.PromptTokens,
		CompletionTokens: p.CompletionTokens,
		TotalTokens:      p.TotalTokens,
		Iterations:       p.Iterations,
		Question:         p.Question,
		Answer:           p.Answer,
		Error:            p.Error,
		StartedAt:        p.StartedAt.UTC().Format(time.RFC3339),
	}
	if !p.EndedAt.IsZero() {
		r.EndedAt = p.EndedAt.UTC().Format(time.RFC3339)
	}
	for _, s := range p.Steps {
		r.Steps = append(r.Steps, RunStep{Seq: s.Seq, Tool: s.Tool, IsError: s.IsError, DurationMS: s.DurationMS})
	}
	return r
}
