package usecases

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// RelatedAtoms finds the claims near one claim without embeddings.
//
// The strongest relation is the shared CLUSTER: claims the association step grouped together are
// about the same subject by construction, and a cluster is many-to-many, so a claim can be near
// claims of several groups. The flat TOPIC is the fallback relation. Other clusters are deliberately
// NOT included through links: this is the claim trail, not page-to-page relatedness (there are no
// pages).
type RelatedAtoms struct {
	Atoms    interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
}

// RelatedAtomsResult keeps the two relations APART. They answer different questions — "what does this
// claim travel with?" and "what else is filed under this subject?" — and a list that mixes them can
// only be read as one ranking. The view shows them as two sections.
type RelatedAtomsResult struct {
	Peers     []partialentities.PartialAtom
	SameTopic []partialentities.PartialAtom
}

// Execute returns claims related to id, grouped: the cluster peers first, then the claims that share
// only its flat topic. The two groups are disjoint.
func (uc RelatedAtoms) Execute(ctx context.Context, id ulid.ULID, limit int) (RelatedAtomsResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	base, err := uc.Atoms.GetAtom(ctx, id)
	if err != nil {
		return RelatedAtomsResult{}, err
	}
	seen := map[ulid.ULID]bool{id: true}
	peers := make([]entities.Atom, 0, limit)
	addPeer := func(a entities.Atom) {
		if seen[a.ID] || len(peers) >= limit {
			return
		}
		seen[a.ID] = true
		peers = append(peers, a)
	}

	if uc.Clusters != nil {
		clusters, cErr := uc.Clusters.ClusterIDsOfAtom(ctx, id)
		if cErr == nil {
			for _, clusterID := range clusters {
				members, mErr := uc.Clusters.AtomsOfCluster(ctx, clusterID)
				if mErr != nil {
					continue
				}
				for _, atomID := range members {
					if seen[atomID] || len(peers) >= limit {
						continue
					}
					if a, gErr := uc.Atoms.GetAtom(ctx, atomID); gErr == nil {
						addPeer(a)
					}
				}
			}
		}
	}

	// The topic list is fetched WIDER than the answer needs: it may contain the claim itself and every
	// peer, and those are not same-topic results.
	topic := make([]entities.Atom, 0, limit)
	if base.Topic != "" {
		topicAtoms, lErr := uc.Atoms.ListAtoms(ctx, base.Topic, limit+len(peers)+1, 0)
		if lErr != nil {
			return RelatedAtomsResult{}, lErr
		}
		for _, a := range topicAtoms {
			if seen[a.ID] || len(topic) >= limit {
				continue
			}
			seen[a.ID] = true
			topic = append(topic, a)
		}
	}

	out := RelatedAtomsResult{
		Peers:     make([]partialentities.PartialAtom, 0, len(peers)),
		SameTopic: make([]partialentities.PartialAtom, 0, len(topic)),
	}
	for _, a := range peers {
		out.Peers = append(out.Peers, partialentities.PartialFromAtom(a))
	}
	for _, a := range topic {
		out.SameTopic = append(out.SameTopic, partialentities.PartialFromAtom(a))
	}
	return out, nil
}
