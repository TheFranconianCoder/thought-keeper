package usecases

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// NeedsRepair answers the start-up consistency question: does the index still agree with the VAULT?
//
// The vault is the truth. The association layer is derived from the claims: every cluster record in
// clusters/ must have its row and its membership, and its LIVE members must agree with the
// membership table. Anything else is repaired by a reindex.
//
// A record that names a claim the vault no longer holds is NOT a disagreement: it is what a
// re-derivation leaves behind. The old atoms are gone (their membership rows with them), and the
// cluster pass prunes the dead record members in its first step. Treating that as an inconsistency
// made every start after a batch re-derivation scan the whole vault three times — and then rebuild
// nothing. Such records are counted (stale) and left alone; they are invisible to retrieval, which
// reads cluster_atoms.
func (uc Maintenance) NeedsRepair(ctx context.Context) (bool, string, int, error) {
	if uc.ClusterStore == nil || uc.ClusterRepo == nil {
		return false, "", 0, nil
	}
	records, err := uc.ClusterStore.ScanMetas(ctx)
	if err != nil {
		return false, "", 0, fmt.Errorf("consistency: scan cluster records: %w", err)
	}
	// The ids only. This check answers "does every member still exist", and reading 170k claim bodies
	// to find out is what made the start-up check the most expensive thing the app did.
	existing, err := uc.Atoms.AtomIDs(ctx)
	if err != nil {
		return false, "", 0, fmt.Errorf("consistency: list claims: %w", err)
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
		return false, "", 0, fmt.Errorf("consistency: list clusters: %w", err)
	}
	rowAtoms := make(map[ulid.ULID][]ulid.ULID, len(rows))
	for _, row := range rows {
		rowAtoms[row.ID] = row.Atoms
	}
	stale := 0
	for _, rec := range records {
		want[rec.ID] = true
		// The members that still exist ARE the record. Dead members are a re-derivation's leftovers.
		live := make([]ulid.ULID, 0, len(rec.Atoms))
		for _, id := range rec.Atoms {
			if exists[id] {
				live = append(live, id)
			}
		}
		if len(live) == 0 {
			stale++
			// A record with no live member is stale — unless its row still holds members the record
			// stopped naming. That is the index serving a cluster the vault can no longer describe.
			if row, ok := rowAtoms[rec.ID]; ok && len(row) > 0 {
				return true, fmt.Sprintf("cluster %s: membership table has members the record does not", rec.ID), stale, nil
			}
			continue
		}
		if _, ok := rowAtoms[rec.ID]; !ok {
			return true, fmt.Sprintf("cluster %s has a record but no row", rec.ID), stale, nil
		}
		if !sameULIDSet(rowAtoms[rec.ID], live) {
			return true, fmt.Sprintf("cluster %s: membership table and record disagree", rec.ID), stale, nil
		}
	}
	for _, row := range rows {
		if want[row.ID] || len(row.Atoms) == 0 {
			// A row without a record is stale when it holds nothing; with members the index knows a
			// cluster the vault does not.
			continue
		}
		return true, fmt.Sprintf("cluster row %s has no record file", row.ID), stale, nil
	}
	return false, "", stale, nil
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
