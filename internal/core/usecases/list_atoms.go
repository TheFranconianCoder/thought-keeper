package usecases

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// ListAtoms lists atoms, optionally filtered by topic.
type ListAtoms struct {
	AtomRepo interfaces.AtomRepository
	// Clusters hydrates each claim's membership: the list row says how many clusters a claim
	// travels in, and a count that is always zero would be a lie.
	Clusters interfaces.ClusterRepository
}

// Execute returns atoms (newest first).
func (uc ListAtoms) Execute(ctx context.Context, branch string, limit, offset int) ([]partialentities.PartialAtom, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	atoms, err := uc.AtomRepo.ListAtoms(ctx, branch, limit, offset)
	if err != nil {
		return nil, err
	}
	// The memberships of the PAGE only. Loading every cluster→atom edge to decorate 200 rows was
	// 248k rows on a grown vault — a whole-table read behind a half-second list.
	membership := map[ulid.ULID][]ulid.ULID{}
	if uc.Clusters != nil && len(atoms) > 0 {
		ids := make([]ulid.ULID, 0, len(atoms))
		for _, a := range atoms {
			ids = append(ids, a.ID)
		}
		if links, cErr := uc.Clusters.MembershipsForAtoms(ctx, ids); cErr == nil {
			for _, l := range links {
				membership[l.AtomID] = append(membership[l.AtomID], l.ClusterID)
			}
		}
	}
	out := make([]partialentities.PartialAtom, 0, len(atoms))
	for _, a := range atoms {
		p := partialentities.PartialFromAtom(a)
		p.Clusters = membership[a.ID]
		out = append(out, p)
	}
	return out, nil
}

// ListClaimTopics lists claim topics with atom counts.
type ListClaimTopics struct {
	AtomRepo interfaces.AtomRepository
}

// Execute returns the claim topics.
func (uc ListClaimTopics) Execute(ctx context.Context) ([]partialentities.PartialBranch, error) {
	branches, err := uc.AtomRepo.ListClaimTopics(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialBranch, 0, len(branches))
	for _, b := range branches {
		out = append(out, partialentities.PartialBranch{Topic: b.Topic, Count: b.Count})
	}
	return out, nil
}

// GetAtom returns one atom with its provenance and the clusters it belongs to. The cluster ids are
// not navigation of their own: the Claims detail view uses them to show the claim's PEERS.
type GetAtom struct {
	AtomRepo interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
}

// Execute looks up an atom by id.
func (uc GetAtom) Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialAtom, error) {
	a, err := uc.AtomRepo.GetAtom(ctx, id)
	if err != nil {
		return partialentities.PartialAtom{}, err
	}
	out := partialentities.PartialFromAtom(a)
	if uc.Clusters != nil {
		if ids, err := uc.Clusters.ClusterIDsOfAtom(ctx, id); err == nil {
			out.Clusters = ids
		}
	}
	return out, nil
}

// GetRun returns one agent run.
type GetRun struct {
	Runs interfaces.RunRepository
}

// Execute looks up a run by id.
func (uc GetRun) Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialRun, error) {
	r, err := uc.Runs.GetRun(ctx, id)
	if err != nil {
		return partialentities.PartialRun{}, err
	}
	out := partialentities.PartialRun{
		ID:               r.ID.String(),
		Op:               string(r.Op),
		Model:            r.Model,
		TargetID:         r.TargetID,
		Status:           string(r.Status),
		PromptTokens:     r.PromptTokens,
		CompletionTokens: r.CompletionTokens,
		TotalTokens:      r.TotalTokens,
		Iterations:       r.Iterations,
		Question:         r.Question,
		Answer:           r.Answer,
		Error:            r.Error,
		StartedAt:        r.StartedAt,
		EndedAt:          r.EndedAt,
	}
	if steps, err := uc.Runs.ListSteps(ctx, id); err == nil {
		for _, s := range steps {
			out.Steps = append(out.Steps, partialentities.PartialRunStep{
				Seq: s.Seq, Tool: s.Tool, IsError: s.IsError, DurationMS: s.DurationMS,
			})
		}
	}
	return out, nil
}
