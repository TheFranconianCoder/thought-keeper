package usecases

import (
	"context"
	"log/slog"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// DeleteSource removes a source and everything that only existed because of it: the indexed row (its
// nodes and provenance cascade), the content-addressed object (moved to the trash), every sidecar next
// to it (rendering, step record, outline, log) and any atom that cited this source and no other.
// Cluster records that cited those atoms are a cache: the next cluster pass prunes the dead members.
type DeleteSource struct {
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Atoms     interfaces.AtomStore
	AtomRepo  interfaces.AtomRepository
	SourceLog interfaces.SourceLog
	// Steps drops the source's step record with the source itself.
	Steps interfaces.StepTracker
	Clock interfaces.Clock
	Log   *slog.Logger
}

// DeleteResult reports what was removed.
type DeleteResult struct {
	AtomsRemoved      int
	DependentsRemoved int
}

// deletion is the state of ONE delete call, shared by the whole cascade. Its important part is the
// single vault scan: finding atoms the index missed is a full walk of atoms/, and doing that once per
// child made deleting a 30-item feed take minutes (and a client that gave up killed the cascade
// halfway). One scan serves every source of the call.
type deletion struct {
	seen      map[string]bool
	vault     []entities.Atom
	scanned   bool
	scanErr   error
	scanTried bool
}

// atomsCiting returns every atom that cites the hash — from the index AND from the vault. The vault
// scan is what catches files the index never indexed; it runs once per delete call.
func (d *deletion) atomsCiting(ctx context.Context, uc DeleteSource, hash string) ([]entities.Atom, int, error) {
	citing, err := uc.AtomRepo.ListAtomsBySource(ctx, hash)
	if err != nil {
		// Without the index answer we cannot know what cites this source: deleting it would leave
		// claims pointing at nothing. Stop instead of guessing.
		return nil, 0, err
	}
	seen := make(map[ulid.ULID]bool, len(citing))
	for _, a := range citing {
		seen[a.ID] = true
	}
	if uc.Atoms == nil {
		return citing, 0, nil
	}
	if !d.scanTried {
		d.scanTried = true
		d.vault, d.scanErr = uc.Atoms.Scan(ctx)
		d.scanned = d.scanErr == nil
		if d.scanErr != nil && uc.Log != nil {
			uc.Log.Warn("delete: the vault could not be scanned for atoms", "err", d.scanErr)
		}
	}
	if !d.scanned {
		return citing, 0, nil
	}
	vaultOnly := 0
	for _, a := range d.vault {
		if seen[a.ID] {
			continue
		}
		for _, src := range a.Sources {
			if src.Hash == hash {
				citing = append(citing, a)
				seen[a.ID] = true
				vaultOnly++
				break
			}
		}
	}
	return citing, vaultOnly, nil
}

// Execute deletes one source by hash. A dependent source (a page a sitemap, a feed or a wiki page
// brought in) is refused: its owner controls its lifetime. Removing a sitemap, a feed or a wiki page
// goes through ExecuteOwned, which cascades.
func (uc DeleteSource) Execute(ctx context.Context, hash string) (DeleteResult, error) {
	return uc.delete(cancelProof(ctx), hash, false, &deletion{seen: map[string]bool{}})
}

// ExecuteOwned removes a source that belongs to an owner. It is the internal cascade path; the API
// never calls it directly.
func (uc DeleteSource) ExecuteOwned(ctx context.Context, hash string) (DeleteResult, error) {
	return uc.delete(cancelProof(ctx), hash, true, &deletion{seen: map[string]bool{}})
}

// cancelProof detaches a DELETE from its request. The human confirmed it; a browser that navigates
// away or gives up must not leave a half-deleted owner behind (that is exactly what happened: a feed
// with 30 children lost its children and kept the feed, because the request context was canceled
// mid-cascade and every later read failed with "context canceled").
func cancelProof(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (uc DeleteSource) delete(ctx context.Context, hash string, allowDependent bool, d *deletion) (DeleteResult, error) {
	if d.seen[hash] {
		return DeleteResult{}, nil
	}
	d.seen[hash] = true
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return DeleteResult{}, err
	}
	if !allowDependent {
		switch {
		case src.Origin == entities.SitemapOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a sitemap; delete the sitemap instead", types.ErrConflict)
		case src.Origin == entities.RSSOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a feed; delete the feed instead", types.ErrConflict)
		case src.Origin == entities.WikiLinkOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a wiki page; delete the page instead", types.ErrConflict)
		case src.IsWiki():
			return DeleteResult{}, types.New("dependent_source",
				"this source is a wiki page relationship; delete the page instead", types.ErrConflict)
		}
	}

	// A sitemap or a feed owns the pages it declared. Removing it removes the whole subtree (a feed's
	// items too: that is the only way its children ever leave); a child sitemap/feed recurses through
	// the same path, and the shared `seen` stops a cycle in a malformed ownership graph. A page that
	// disappeared from a FEED was kept while the feed existed — deleting the feed is what deletes it.
	var dependents DeleteResult
	if src.OwnsChildren() {
		rows, listErr := uc.Index.ListURLSources(ctx)
		if listErr != nil {
			if uc.Log != nil {
				uc.Log.Warn("delete owner: children not listed", "hash", hash, "err", listErr)
			}
		} else {
			for _, child := range rows {
				if child.ParentHash != hash || child.Origin == "" {
					continue
				}
				removed, childErr := uc.delete(ctx, child.Hash, true, d)
				if childErr != nil {
					if uc.Log != nil {
						uc.Log.Warn("delete owner: child not removed", "owner", hash, "child", child.Hash, "err", childErr)
					}
					continue
				}
				dependents.DependentsRemoved += 1 + removed.DependentsRemoved
				dependents.AtomsRemoved += removed.AtomsRemoved
			}
		}
	}
	citing, vaultOnly, err := d.atomsCiting(ctx, uc, hash)
	if err != nil {
		return DeleteResult{}, err
	}

	// The deletion is the source's last step: write it down before the work below unwinds
	// (the row goes, the log file stays in the index, next to the source's history).
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "delete", Level: "warn", Message: "source deleted",
			Fields: map[string]any{
				"kind": src.Kind, "title": src.Title, "original": src.OriginalName,
				"atoms": len(citing), "imported_at": src.ImportedAt.Format("2006-01-02T15:04:05Z"),
			},
		})
	}

	if err := uc.Index.DeleteSourceByHash(ctx, hash); err != nil {
		return DeleteResult{}, err
	}

	res := DeleteResult{
		AtomsRemoved:      dependents.AtomsRemoved,
		DependentsRemoved: dependents.DependentsRemoved,
	}
	for _, a := range citing {
		n, err := uc.AtomRepo.CountAtomSources(ctx, a.ID)
		if err != nil || n > 0 {
			continue
		}
		if err := uc.AtomRepo.DeleteAtom(ctx, a.ID); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("delete source: atom row", "atom", a.ID.String(), "err", err)
			}
			continue
		}
		if err := uc.Atoms.Remove(ctx, a); err != nil && uc.Log != nil {
			uc.Log.Warn("delete source: atom file", "atom", a.ID.String(), "err", err)
		}
		res.AtomsRemoved++
	}

	// The whole story of the source goes with it: the object (to the trash — bytes are recoverable,
	// but the source no longer exists) and EVERY sidecar next to it — the readable rendering, the
	// step record, the outline and the log. A source is one folder entry; deleting it deletes what
	// belongs to it, so nothing of a source that no longer exists can linger and be mistaken for
	// knowledge.
	if uc.SourceLog != nil {
		// Last line before the log is removed: what a deletion leaves behind is in the PROCESS log
		// (the per-source file is gone with the source).
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "delete", Level: "warn", Message: "deletion finished",
			Fields: map[string]any{
				"atoms_removed": res.AtomsRemoved, "object": "moved to trash",
			},
		})
	}
	if err := uc.Store.Remove(ctx, hash, src.Ext); err != nil && uc.Log != nil {
		uc.Log.Warn("delete source: object", "hash", hash, "err", err)
	}
	if err := uc.Store.RemoveSidecars(hash, src.Ext); err != nil && uc.Log != nil {
		uc.Log.Warn("delete source: sidecars", "hash", hash, "err", err)
	}
	if uc.Log != nil {
		uc.Log.Info("source deleted", "hash", hash, "kind", string(src.Kind),
			"atoms", res.AtomsRemoved, "atoms_from_vault", vaultOnly,
			"sidecars", "removed", "object", "trashed")
	}
	if uc.Steps != nil {
		if err := uc.Steps.Remove(hash); err != nil && uc.Log != nil {
			uc.Log.Warn("delete: drop the step record", "hash", hash, "err", err)
		}
	}
	return res, nil
}
