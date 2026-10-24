package usecases

import (
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestClaimGraphKeepsDistinctCppSubjectsApart(t *testing.T) {
	claims := []struct {
		topic string
		text  string
	}{
		{"raii", "RAII binds resource lifetime to object lifetime."},
		{"raii", "A destructor releases the resource acquired by the constructor."},
		{"raii", "Smart pointers implement RAII by owning the pointee."},
		{"raii", "Resource acquisition is initialization prevents leaks."},
		{"std-containers", "std::vector stores elements contiguously in memory."},
		{"std-containers", "std::map is a balanced tree of keys and values."},
		{"std-containers", "A std::string owns a character buffer."},
		{"std-containers", "Iterators traverse the standard containers."},
		{"move-semantics", "Move semantics transfer ownership without copying."},
		{"move-semantics", "The move constructor steals the source pointer."},
		{"move-semantics", "std::move casts an object to an rvalue reference."},
		{"move-semantics", "The rule of five defines copy and move operations."},
	}
	atoms := make([]entities.Atom, 0, len(claims))
	for _, c := range claims {
		atoms = append(atoms, entities.Atom{ID: ulid.Make(), Topic: c.topic, Claim: c.text, Body: c.text})
	}
	g := buildClaimGraph(atoms, ClusterSettings{})
	candidates := g.candidates()
	if len(candidates) < 2 {
		t.Fatalf("candidates=%d, want the distinct C++ subjects separated", len(candidates))
	}
	for _, c := range candidates {
		if c.Metrics.Topics > 1 && len(c.Members) > 4 {
			t.Errorf("cluster spans %d topics with %d claims: %v", c.Metrics.Topics, len(c.Members), c.Metrics.CoreTerms)
		}
	}
}

func TestClaimGraphKeepsOneSmallSubjectTogether(t *testing.T) {
	atoms := make([]entities.Atom, 0, 3)
	for range 3 {
		atoms = append(atoms, entities.Atom{
			ID: ulid.Make(), Topic: "caching",
			Claim: "Cache invalidation keeps a cache coherent with its source.",
			Body:  "Invalidate the entry when the source changes.",
		})
	}
	g := buildClaimGraph(atoms, ClusterSettings{})
	candidates := g.candidates()
	if len(candidates) != 1 || len(candidates[0].Members) != 3 {
		t.Fatalf("candidates = %+v, want one three-claim cluster", candidates)
	}
}

func TestAddOverlapUsesRealNeighboursNotTheTopic(t *testing.T) {
	// One term shared by every claim: the content test alone would let the bridge into BOTH
	// clusters. B wins because B is where the bridge's four real edges point.
	atoms := make([]entities.Atom, 8)
	for i := range atoms {
		atoms[i] = entities.Atom{ID: ulid.Make(), Topic: "caching", Claim: "cache"}
	}
	g := &claimGraph{atoms: atoms, corpus: buildClaimCorpus(atoms), neighbors: make([]neighborList, 8)}
	// The bridge shares content with B and has four real edges into it. B needs four members so
	// that four edges are possible; A stays a separate cluster.
	g.neighbors[7] = neighborList{at: []int32{3, 4, 5, 6}, score: []float64{0.3, 0.3, 0.3, 0.3}}
	assignments := []clusterAssignment{
		{Title: "A", Members: []int{0, 1, 2}},
		{Title: "B", Members: []int{3, 4, 5, 6}},
		{Title: "Bridge", Members: []int{7}},
	}
	out := addOverlap(assignments, g, ClusterSettings{OverlapMax: 2, OverlapMinEdges: 4, AttachMinScore: 0.18, MaxClusterClaims: 80})
	found := 0
	for _, assignment := range out {
		for _, member := range assignment.Members {
			if member == 7 && assignment.Title != "Bridge" {
				found++
			}
		}
	}
	if found != 1 {
		t.Fatalf("bridge claim is in %d extra clusters, want exactly one: %+v", found, out)
	}
}

func TestCrossTopicMergeNeedsSeveralRealClaimEdges(t *testing.T) {
	atoms := make([]entities.Atom, 0, 6)
	for range 1 {
		atoms = append(atoms, entities.Atom{
			ID: ulid.Make(), Topic: "raii",
			Claim: "RAII binds resource lifetime to object lifetime.",
			Body:  "A destructor releases the resource acquired by the constructor.",
		})
	}
	for range 3 {
		atoms = append(atoms, entities.Atom{
			ID: ulid.Make(), Topic: "resource-management",
			Claim: "RAII binds resource lifetime to object lifetime.",
			Body:  "A destructor releases the resource acquired by the constructor.",
		})
	}
	g := buildClaimGraph(atoms, ClusterSettings{})
	candidates := g.candidates()
	if len(candidates) != 1 || candidates[0].Metrics.Topics != 2 {
		t.Fatalf("candidates = %+v, want one cross-topic subject", candidates)
	}
}

func TestSplitBroadCutsAcrossTopics(t *testing.T) {
	atoms := make([]entities.Atom, 0, 20)
	for i := range 20 {
		atoms = append(atoms, entities.Atom{
			ID:    ulid.Make(),
			Topic: "topic-" + string(rune('a'+i)),
			Claim: "Claim about subject " + string(rune('a'+i)),
		})
	}
	g := &claimGraph{atoms: atoms}
	groups := g.splitBroad([][]int{func() []int {
		out := make([]int, len(atoms))
		for i := range out {
			out[i] = i
		}
		return out
	}()}, ClusterSettings{TopicSpanLimit: 12, MaxClusterClaims: 80})
	total := 0
	for _, group := range groups {
		total += len(group)
		topics := map[string]bool{}
		for _, member := range group {
			topics[g.atoms[member].Topic] = true
		}
		if len(topics) > 12 {
			t.Errorf("a split group still spans %d topics: %v", len(topics), topics)
		}
	}
	if total != len(atoms) {
		t.Errorf("split lost claims: %d of %d", total, len(atoms))
	}
}
