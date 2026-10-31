package usecases

import (
	"context"
	"log/slog"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// Claims is the use case of the claim corpus: the atoms as files, the index rows that list them, and
// the repairs that keep the two in step.
type Claims struct {
	Atoms        interfaces.AtomRepository
	AtomStore    interfaces.AtomStore
	Clusters     interfaces.ClusterRepository
	CommandStore interfaces.CommandStore
	Vocab        interfaces.VocabularyStore
	Gateway      interfaces.AgentGateway
	Model        string
	Runs         interfaces.RunRepository
	Clock        interfaces.Clock
	Log          *slog.Logger
	OnPhase      func(phase string)
	MaxClaims    int
	Parallel     int
	Apply        bool
}

// AtomLister lists the claims of one topic. A handler holds this value; nil means the feature is not
// wired.
type AtomLister func(ctx context.Context, topic string, limit, offset int) ([]partialentities.PartialAtom, error)

// List returns one page of claims (newest first), each with its memberships.
func (uc Claims) List(ctx context.Context, branch string, limit, offset int) ([]partialentities.PartialAtom, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	atoms, err := uc.Atoms.ListAtoms(ctx, branch, limit, offset)
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

// ClaimTopicLister lists the topics the corpus uses, with their counts. A handler holds this value;
// nil means the feature is not wired.
type ClaimTopicLister func(ctx context.Context) ([]partialentities.PartialBranch, error)

// Topics returns the topics the corpus uses.
func (uc Claims) Topics(ctx context.Context) ([]partialentities.PartialBranch, error) {
	branches, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialBranch, 0, len(branches))
	for _, b := range branches {
		out = append(out, partialentities.PartialBranch{Topic: b.Topic, Count: b.Count})
	}
	return out, nil
}

// AtomGetter returns one claim, with its peers and its memberships. A handler holds this value; nil
// means the feature is not wired.
type AtomGetter func(ctx context.Context, id ulid.ULID) (partialentities.PartialAtom, error)

// Get returns one claim by id.
func (uc Claims) Get(ctx context.Context, id ulid.ULID) (partialentities.PartialAtom, error) {
	a, err := uc.Atoms.GetAtom(ctx, id)
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

// RunGetter looks up one run by id. A handler holds this value; nil means the feature is not wired.
type RunGetter func(ctx context.Context, id ulid.ULID) (partialentities.PartialRun, error)

// Get looks up a run by id.
func (uc Runs) Get(ctx context.Context, id ulid.ULID) (partialentities.PartialRun, error) {
	r, err := uc.Store.GetRun(ctx, id)
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
	if steps, err := uc.Store.ListSteps(ctx, id); err == nil {
		for _, s := range steps {
			out.Steps = append(out.Steps, partialentities.PartialRunStep{
				Seq: s.Seq, Tool: s.Tool, IsError: s.IsError, DurationMS: s.DurationMS,
			})
		}
	}
	return out, nil
}
