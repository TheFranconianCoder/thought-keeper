package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// Cluster is one TITLELESS group of related claims. It is the association the pipeline's fourth
// step (cluster) derives from the claims themselves: the claims sharing a subject become neighbours
// of each other, and that neighbourhood — not a rendered page — is what the vault keeps.
//
// A cluster deliberately has no title, no kind and no tags. Those only ever existed to name a rendered
// page; with the page layer gone there is nothing left to name, and a title would drag the whole
// model-titling machinery (and its failure modes) back in. The signature is the identity of the
// CLAIM SET, so a caller can tell whether a cluster still groups the same claims or must be
// re-derived.
//
// It is a CACHE, not a record: the atoms are the truth, the graph over them is deterministic, and
// reindex can rebuild every cluster from the claims alone. That is why there is no dirty marker, no
// successor chain and no retired state: a cluster that goes stale is simply re-derived, and nothing
// links to it by id the way a page used to.
type Cluster struct {
	ID    ulid.ULID
	Atoms []ulid.ULID
	// Signature is a stable hash over the sorted member ids — the cheap "did the membership change?"
	// test a re-derivation and the membership lint both read.
	Signature string
	Created   time.Time
	Updated   time.Time
}

// AtomIDs is the cluster's membership in id order (defensive: callers must not mutate the slice).
func (c Cluster) AtomIDs() []ulid.ULID {
	return append([]ulid.ULID(nil), c.Atoms...)
}

// ClusterMembership is one cluster→atom edge, the mirror of a record's `atoms` list. It is what
// answers "which cluster is this claim in?" and "what are this claim's cluster peers?" — the two
// reads the Claims view and Ask navigate by.
type ClusterMembership struct {
	ClusterID ulid.ULID
	AtomID    ulid.ULID
}
