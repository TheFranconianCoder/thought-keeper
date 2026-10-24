package usecases

import (
	"context"
	"sort"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// SourceAtoms lists the atoms extracted from one source (its provenance in the
// middle layer), so a source view can show what it produced.
type SourceAtoms struct {
	AtomRepo interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
}

// SourceTopicUse is one of a source's claim topics with how many of its claims carry it.
type SourceTopicUse struct {
	Name  string
	Count int
}

// SourceAtomsResult is what the source page needs about a source's claims: a BOUNDED list (a book
// has hundreds, and shipping all of them with their bodies is what made opening the page feel
// slow), the total count, and the claim topics with their counts — computed over ALL of them, so
// the rail shows the real picture without the payload.
type SourceAtomsResult struct {
	Atoms  []partialentities.PartialAtom
	Total  int
	Topics []SourceTopicUse
}

// DefaultSourceAtomsLimit bounds the claims one overlay request returns.
const DefaultSourceAtomsLimit = 100

// Execute returns the atoms citing the source with this hash, at most `limit` of them.
func (uc SourceAtoms) Execute(ctx context.Context, hash string, limit int) (SourceAtomsResult, error) {
	if limit <= 0 {
		limit = DefaultSourceAtomsLimit
	}
	atoms, err := uc.AtomRepo.ListAtomsBySource(ctx, hash)
	if err != nil {
		return SourceAtomsResult{}, err
	}
	res := SourceAtomsResult{Total: len(atoms)}
	// The rail describes the WHOLE source, not the bounded slice: the topics are counted here, over
	// every claim, and travel as one short list. Sorted by COUNT, so "what is this source about" is
	// answered by the head of the list instead of the alphabet.
	counts := map[string]int{}
	for _, a := range atoms {
		if a.Topic != "" {
			counts[a.Topic]++
		}
	}
	res.Topics = make([]SourceTopicUse, 0, len(counts))
	for name, n := range counts {
		res.Topics = append(res.Topics, SourceTopicUse{Name: name, Count: n})
	}
	sort.Slice(res.Topics, func(i, j int) bool {
		if res.Topics[i].Count != res.Topics[j].Count {
			return res.Topics[i].Count > res.Topics[j].Count
		}
		return res.Topics[i].Name < res.Topics[j].Name
	})
	if len(atoms) > limit {
		atoms = atoms[:limit]
	}
	// Only the claims this answer carries, and none at all when the source has none — the old code
	// loaded every membership in the vault to answer a 23-byte "no claims" response.
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
	res.Atoms = make([]partialentities.PartialAtom, 0, len(atoms))
	for _, a := range atoms {
		p := partialentities.PartialFromAtom(a)
		p.Clusters = membership[a.ID]
		res.Atoms = append(res.Atoms, p)
	}
	return res, nil
}
