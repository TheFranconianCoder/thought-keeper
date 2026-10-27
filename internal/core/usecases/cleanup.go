package usecases

import (
	"context"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Cleanup repairs the association layer: it prunes cluster records whose claims are gone, drops empty
// clusters, and refreshes the programmatic findings. It is deterministic and safe to run at the end
// of a cluster pass; there are no pages to delete or reconcile any more.
type Cleanup struct {
	Clusters interfaces.ClusterRepository
	Store    interfaces.ClusterStore
	Atoms    interfaces.AtomRepository
	Lint     interfaces.LintRepository
	Clock    interfaces.Clock
	Log      *slog.Logger
}

// CleanupResult reports what was repaired.
type CleanupResult struct {
	ClustersPruned  int
	ClustersRemoved int
	Findings        int
}

// Execute runs the repair pass.
func (uc Cleanup) Execute(ctx context.Context) (CleanupResult, error) {
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
	records, err := uc.Store.ScanMetas(ctx)
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
			if err := uc.Store.RemoveMeta(ctx, rec.ID); err != nil {
				uc.warn("remove empty cluster", rec.ID, err)
				continue
			}
			_ = uc.Clusters.DeleteCluster(ctx, rec.ID)
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
			if err := uc.Store.WriteMeta(ctx, rec); err != nil {
				uc.warn("rewrite pruned cluster", rec.ID, err)
				continue
			}
			if err := uc.Clusters.SaveCluster(ctx, rec); err != nil {
				uc.warn("index pruned cluster", rec.ID, err)
				continue
			}
			res.ClustersPruned++
		}
	}

	if uc.Lint != nil {
		if err := uc.Lint.DeleteFindingsByLayer(ctx, entities.FindingProgrammatic); err == nil {
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

func (uc Cleanup) now() time.Time {
	if uc.Clock != nil {
		return uc.Clock.Now()
	}
	return time.Now().UTC()
}

func (uc Cleanup) warn(what string, id ulid.ULID, err error) {
	if uc.Log != nil {
		uc.Log.Warn("cleanup: "+what, "cluster", id.String(), "err", err)
	}
}
