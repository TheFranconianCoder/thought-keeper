package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// CleanupResult reports what was repaired.
type CleanupResult struct {
	ClustersPruned  int
	ClustersRemoved int
	Findings        int
}

// Cleaning prunes the dead members of the derived layer. A handler holds this value; nil means the
// feature is not wired.
type Cleaning func(ctx context.Context) (CleanupResult, error)

// Cleanup prunes what the derived layer holds for rows that are gone. A write that fails is
// RETURNED, not swallowed: a half-pruned association layer is exactly the inconsistency the next
// start rebuilds the whole index for, and a silent success hides the cause.
func (uc Maintenance) Cleanup(ctx context.Context) (CleanupResult, error) {
	var res CleanupResult
	// The existence set only. A cluster record that keeps all its members is not touched at all, so
	// the common shape of this pass reads no claim body — and the bodies are what the whole-vault
	// list read used to load on every run.
	existing, err := uc.Atoms.AtomIDs(ctx)
	if err != nil {
		return res, err
	}
	present := make(map[string]bool, len(existing))
	for _, id := range existing {
		present[id.String()] = true
	}
	records, err := uc.ClusterStore.ScanMetas(ctx)
	if err != nil {
		return res, err
	}
	now := uc.now()

	// First pass: find the records that actually lost a member. Only those need the text of the
	// claims that survived, because the signature is computed from it.
	type pruned struct {
		rec  entities.Cluster
		kept []ulid.ULID
	}
	var lost []pruned
	dropped := 0
	for _, rec := range records {
		kept := make([]ulid.ULID, 0, len(rec.Atoms))
		for _, id := range rec.Atoms {
			if present[id.String()] {
				kept = append(kept, id)
			}
		}
		if len(kept) == len(rec.Atoms) {
			continue
		}
		if len(kept) == 0 {
			if err := uc.ClusterStore.RemoveMeta(ctx, rec.ID); err != nil {
				return res, fmt.Errorf("cleanup: remove the empty cluster record %s: %w", rec.ID, err)
			}
			if err := uc.ClusterRepo.DeleteCluster(ctx, rec.ID); err != nil {
				return res, fmt.Errorf("cleanup: delete the empty cluster %s: %w", rec.ID, err)
			}
			dropped++
			continue
		}
		rec.Atoms = kept
		lost = append(lost, pruned{rec: rec, kept: kept})
	}
	res.ClustersRemoved = dropped

	// Second pass, only when there is something to repair: the survivors' TEXT only — the claims a
	// pruned record keeps, not the corpus. One read for all changed records.
	if len(lost) > 0 {
		ids := make([]ulid.ULID, 0, len(lost)*8)
		for i := range lost {
			ids = append(ids, lost[i].kept...)
		}
		atoms, err := uc.Atoms.AtomTextsForIDs(ctx, uniqueULIDs(ids))
		if err != nil {
			return res, err
		}
		byID := make(map[string]entities.Atom, len(atoms))
		for _, a := range atoms {
			byID[a.ID.String()] = a
		}
		for i := range lost {
			rec := lost[i].rec
			rec.Signature = subjectSignature(atomsFor(lost[i].kept, byID))
			rec.Updated = now
			if err := uc.ClusterStore.WriteMeta(ctx, rec); err != nil {
				return res, fmt.Errorf("cleanup: rewrite the pruned cluster %s: %w", rec.ID, err)
			}
			if err := uc.ClusterRepo.SaveCluster(ctx, rec); err != nil {
				return res, fmt.Errorf("cleanup: index the pruned cluster %s: %w", rec.ID, err)
			}
			res.ClustersPruned++
		}
	}

	if uc.Lint != nil {
		if err := uc.Lint.DeleteFindingsByLayer(ctx, entities.FindingProgrammatic); err != nil {
			// The findings are a REPORT, not a record of the vault: a failed refresh leaves them
			// stale until the next pass. It is said, not raised.
			if uc.Log != nil {
				uc.Log.Warn("cleanup: the findings could not be refreshed", "err", err)
			}
		} else {
			findings := ProgrammaticFindings(ctx, uc.Lint, uc.Atoms, ulid.ULID{}, uc.now())
			if err := uc.Lint.AddFindings(ctx, findings); err != nil && uc.Log != nil {
				uc.Log.Warn("cleanup: write findings", "err", err)
			}
			res.Findings = len(findings)
		}
	}
	if uc.Log != nil {
		uc.Log.Info("cleanup done", "clusters_pruned", res.ClustersPruned,
			"clusters_removed", res.ClustersRemoved, "findings", res.Findings)
	}
	return res, nil
}

func (uc Maintenance) now() time.Time {
	if uc.Clock != nil {
		return uc.Clock.Now()
	}
	return time.Now().UTC()
}
