package usecases

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ClusterProgress is the live state of one cluster run. It is a report, not persisted state: the app
// turns it into the Job Queue row while the run is in flight. There are no model calls any more, so
// the phases are structural: attach existing members, graph the rest, write the records.
type ClusterProgress struct {
	Phase       string
	TopicsDone  int
	TopicsTotal int
	ClaimsDone  int
	ClaimsTotal int
	Remaining   int
	Clusters    int
	// Attached is how many free claims joined an existing cluster; New is how many formed a fresh one.
	Attached int
	New      int
	Complete bool
}

// subjectSignature is the stable hash of a claim set: the cheap "is this still the same cluster?"
// test. It covers id, claim and body so a reworded claim invalidates the signature, not just a
// membership change.
func subjectSignature(atoms []entities.Atom) string {
	parts := make([]string, 0, len(atoms))
	for _, a := range atoms {
		parts = append(parts, a.ID.String()+"\x1f"+a.Claim+"\x1f"+a.Body)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(sum[:])
}

func topicCount(atoms []entities.Atom) int {
	seen := map[string]bool{}
	for _, a := range atoms {
		topic := strings.TrimSpace(a.Topic)
		if topic == "" {
			continue
		}
		seen[topic] = true
	}
	return len(seen)
}

func uniqueULIDs(ids []ulid.ULID) []ulid.ULID {
	seen := make(map[ulid.ULID]bool, len(ids))
	out := make([]ulid.ULID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func unionULIDs(existing []ulid.ULID, added []ulid.ULID) []ulid.ULID {
	return uniqueULIDs(append(append([]ulid.ULID(nil), existing...), added...))
}

func atomsFor(ids []ulid.ULID, byID map[string]entities.Atom) []entities.Atom {
	out := make([]entities.Atom, 0, len(ids))
	for _, id := range ids {
		if a, ok := byID[id.String()]; ok {
			out = append(out, a)
		}
	}
	return out
}
