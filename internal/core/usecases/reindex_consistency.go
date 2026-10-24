package usecases

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// NeedsRepair answers the start-up consistency question: does the index still agree with the VAULT?
//
// The vault is the truth. The association layer is a CACHE derived from the claims: every cluster
// record in clusters/ must have its row and its membership, its members must exist as claims, and the
// membership rows must not name a claim the record does not. Anything else is repaired by a reindex,
// which rebuilds clusters from the records and prunes what has no claim.
//
// This check is deliberately cheap: it reads the cluster records and the membership table, not the
// claim bodies.
func (uc ReindexVault) NeedsRepair(ctx context.Context) (bool, string, error) {
	if uc.ClusterStore == nil || uc.ClusterRepo == nil {
		return false, "", nil
	}
	records, err := uc.ClusterStore.ScanMetas(ctx)
	if err != nil {
		return false, "", fmt.Errorf("consistency: scan cluster records: %w", err)
	}
	// The ids only. This check answers "does every member still exist", and reading 170k claim bodies
	// to find out is what made the start-up check the most expensive thing the app did.
	existing, err := uc.Atoms.AtomIDs(ctx)
	if err != nil {
		return false, "", fmt.Errorf("consistency: list claims: %w", err)
	}
	exists := make(map[ulid.ULID]bool, len(existing))
	for _, id := range existing {
		exists[id] = true
	}
	want := map[ulid.ULID]bool{}
	// Rows and memberships in ONE pass. The per-record GetCluster/AtomsOfCluster loop asked the
	// index 26k questions at start-up to answer one ("does the index still agree with the vault?");
	// ListClusters now carries membership from a single bulk read.
	rows, err := uc.ClusterRepo.ListClusters(ctx, 1_000_000, 0)
	if err != nil {
		return false, "", fmt.Errorf("consistency: list clusters: %w", err)
	}
	rowAtoms := make(map[ulid.ULID][]ulid.ULID, len(rows))
	for _, row := range rows {
		rowAtoms[row.ID] = row.Atoms
	}
	for _, rec := range records {
		if _, ok := rowAtoms[rec.ID]; !ok {
			return true, fmt.Sprintf("cluster %s has a record but no row", rec.ID), nil
		}
		want[rec.ID] = true
		for _, id := range rec.Atoms {
			if !exists[id] {
				return true, fmt.Sprintf("cluster %s names claim %s, which is gone", rec.ID, id), nil
			}
		}
		if !sameULIDSet(rowAtoms[rec.ID], rec.Atoms) {
			return true, fmt.Sprintf("cluster %s: membership table and record disagree", rec.ID), nil
		}
	}
	for _, row := range rows {
		if !want[row.ID] {
			return true, fmt.Sprintf("cluster row %s has no record file", row.ID), nil
		}
	}
	return false, "", nil
}

// sameULIDSet compares two id lists as SETS: order and duplicates are not a disagreement.
func sameULIDSet(a, b []ulid.ULID) bool {
	set := make(map[ulid.ULID]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	other := make(map[ulid.ULID]bool, len(b))
	for _, id := range b {
		other[id] = true
	}
	if len(set) != len(other) {
		return false
	}
	for id := range other {
		if !set[id] {
			return false
		}
	}
	return true
}
