package usecases_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

func TestRelatedAtomsUsesTheFlatTopicBeforeCompile(t *testing.T) {
	ctx := context.Background()
	base := entities.Atom{ID: ulid.Make(), Topic: "caching", Claim: "base"}
	other := entities.Atom{ID: ulid.Make(), Topic: "caching", Claim: "other"}
	unrelated := entities.Atom{ID: ulid.Make(), Topic: "compilers", Claim: "unrelated"}
	repo := &fakeAtomRepo{saved: []entities.Atom{base, other, unrelated}}

	got, err := (usecases.RelatedAtoms{Atoms: repo}).Execute(ctx, base.ID, 10)
	if err != nil {
		t.Fatalf("related: %v", err)
	}
	if len(got.Peers) != 0 {
		t.Fatalf("peers = %+v, want none before the cluster step", got.Peers)
	}
	if len(got.SameTopic) != 1 || got.SameTopic[0].ID != other.ID {
		t.Fatalf("same topic = %+v, want only the same-topic claim", got.SameTopic)
	}
}

// relatedClusters is the two cluster questions the relation asks; the rest of the repository is not
// exercised here.
type relatedClusters struct {
	interfaces.ClusterRepository
	byAtom  map[ulid.ULID][]ulid.ULID
	members map[ulid.ULID][]ulid.ULID
}

func (r relatedClusters) ClusterIDsOfAtom(_ context.Context, id ulid.ULID) ([]ulid.ULID, error) {
	return r.byAtom[id], nil
}

func (r relatedClusters) AtomsOfCluster(_ context.Context, id ulid.ULID) ([]ulid.ULID, error) {
	return r.members[id], nil
}

func TestRelatedAtomsSeparatesPeersFromTheTopic(t *testing.T) {
	ctx := context.Background()
	base := entities.Atom{ID: ulid.Make(), Topic: "caching", Claim: "base"}
	peer := entities.Atom{ID: ulid.Make(), Topic: "caching", Claim: "peer"}
	topicOnly := entities.Atom{ID: ulid.Make(), Topic: "caching", Claim: "topic only"}
	repo := &fakeAtomRepo{saved: []entities.Atom{base, peer, topicOnly}}
	cluster := ulid.Make()
	clusters := relatedClusters{
		byAtom:  map[ulid.ULID][]ulid.ULID{base.ID: {cluster}},
		members: map[ulid.ULID][]ulid.ULID{cluster: {base.ID, peer.ID}},
	}

	got, err := (usecases.RelatedAtoms{Atoms: repo, Clusters: clusters}).Execute(ctx, base.ID, 10)
	if err != nil {
		t.Fatalf("related: %v", err)
	}
	if len(got.Peers) != 1 || got.Peers[0].ID != peer.ID {
		t.Fatalf("peers = %+v, want the cluster peer", got.Peers)
	}
	if len(got.SameTopic) != 1 || got.SameTopic[0].ID != topicOnly.ID {
		t.Fatalf("same topic = %+v, want the topic claim WITHOUT the peer", got.SameTopic)
	}
}
