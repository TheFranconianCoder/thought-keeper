package usecases

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// CleanResult reports what a clean did.
type CleanResult struct {
	Atoms int
}

// SourceCleaner removes a source's derivation and keeps the bytes.
type SourceCleaner func(ctx context.Context, hash string) (CleanResult, error)

// Clean removes a source's derivation.
func (uc Derivation) Clean(ctx context.Context, hash string) (CleanResult, error) {
	return uc.drop(ctx, hash, nil)
}

// SourceCorrector removes the claims a rebuilt outline invalidated and keeps the rest.
type SourceCorrector func(ctx context.Context, hash string, keep map[string]bool) (CleanResult, error)

// Correct removes the claims a rebuilt outline invalidated.
func (uc Derivation) Correct(ctx context.Context, hash string, keep map[string]bool) (CleanResult, error) {
	return uc.drop(ctx, hash, keep)
}

// drop removes a source's claims — all of them, or every claim that does not cite a kept node.
func (uc Derivation) drop(ctx context.Context, hash string, keep map[string]bool) (CleanResult, error) {
	atoms, err := uc.Atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		return CleanResult{}, err
	}
	// A RESET clears the derived bookkeeping even when the source has no CLAIM. A source-relative
	// document has a done part and no atom; leaving that record behind made the next run skip its
	// only part while the pipeline kept asking for a derivation that had already happened.
	if len(atoms) == 0 {
		if keep == nil {
			uc.clearDerived(ctx, hash)
			uc.logCleaned(ctx, hash, 0, keep)
		}
		return CleanResult{}, nil
	}
	removed := make(map[ulid.ULID]bool, len(atoms))
	if keep == nil {
		for _, a := range atoms {
			removed[a.ID] = true
		}
	} else {
		refs, err := uc.Atoms.NodeRefsBySource(ctx, hash)
		if err != nil {
			return CleanResult{}, err
		}
		for _, a := range atoms {
			survives := false
			for _, node := range refs[a.ID] {
				if keep[node] {
					survives = true
					break
				}
			}
			if !survives {
				removed[a.ID] = true
			}
		}
	}
	if len(removed) == 0 {
		return CleanResult{}, nil
	}

	if keep == nil {
		uc.clearDerived(ctx, hash)
	} else if uc.Store != nil {
		// The summary was written from the claims that are leaving: keeping it would leave prose
		// describing a derivation that no longer exists. The next complete distill writes a new one.
		// A correction does NOT clear the derived part records: the rebuild re-arms exactly the
		// parts it changed.
		if err := uc.Store.RemoveRendered(hash, entities.SidecarSummary); err != nil && uc.Log != nil {
			uc.Log.Warn("clean source: the summary could not be removed", "hash", hash, "err", err)
		}
	}

	res := CleanResult{}
	for _, a := range atoms {
		if !removed[a.ID] {
			continue
		}
		if err := uc.Atoms.DeleteAtom(ctx, a.ID); err != nil {
			uc.warn("delete atom", a.ID, err)
			continue
		}
		_ = uc.AtomStore.Remove(ctx, a) //nolint:errcheck // a trash move is best effort; a missing file is the goal
		res.Atoms++
	}

	uc.logCleaned(ctx, hash, res.Atoms, keep)
	return res, nil
}

// clearDerived throws away what a derivation LEAVES BEHIND next to the claims: the summary, the
// agent's auto tags and the recorded part states. A reset must do this even when the source produced
// no claim at all (a source-relative document, a link list): "no atoms" is not "nothing to clear".
func (uc Derivation) clearDerived(ctx context.Context, hash string) {
	if uc.Store != nil {
		// RemoveRendered treats a missing file as done: a source without a summary loses nothing.
		if err := uc.Store.RemoveRendered(hash, entities.SidecarSummary); err != nil && uc.Log != nil {
			uc.Log.Warn("clean source: the summary could not be removed", "hash", hash, "err", err)
		}
	}
	if uc.Tags != nil {
		// Auto tags describe the claims that are leaving. The next complete distill derives new ones;
		// the human labels stay (they are the reader's, not the derivation's).
		if err := uc.Tags.SetSourceAutoTags(ctx, hash, nil, time.Time{}); err != nil && uc.Log != nil {
			uc.Log.Warn("clean source: the auto tags could not be cleared", "hash", hash, "err", err)
		}
	}
	if uc.PartRepo != nil {
		// A reset starts the derivation over: stale "done" records would skip parts.
		if uc.Index != nil {
			if src, err := uc.Index.GetSourceByHash(ctx, hash); err == nil {
				if err := uc.PartRepo.ClearParts(ctx, src.ID); err != nil && uc.Log != nil {
					uc.Log.Warn("clean source: clear part states", "hash", hash, "err", err)
				}
			}
		}
	}
}

// logCleaned writes the one line that says what this clean did. A reset with no claim is still a
// reset, and its line belongs in the source's log like any other.
func (uc Derivation) logCleaned(ctx context.Context, hash string, atoms int, keep map[string]bool) {
	if uc.SourceLog != nil {
		phase, msg := "clean", "previous derivation removed (reset)"
		if keep != nil {
			phase, msg = "clean", "derivation corrected (the changed parts were dropped)"
		}
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: phase, Level: "info",
			Message: msg,
			Fields:  map[string]any{"atoms": atoms},
		})
	}
	if uc.Log != nil {
		uc.Log.Info("source cleaned", "hash", hash, "atoms", atoms)
	}
}

func (uc Derivation) warn(what string, id ulid.ULID, err error) {
	if uc.Log != nil {
		uc.Log.Warn("clean source: "+what, "id", id.String(), "err", err)
	}
}

var _ = entities.Atom{}
