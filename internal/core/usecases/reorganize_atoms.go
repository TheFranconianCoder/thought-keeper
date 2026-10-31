package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// AtomLayoutReader reports the OLD claim layout a vault still holds (branch folders). A handler holds
// this value; nil means the feature is not wired.
type AtomLayoutReader func(ctx context.Context) (partialentities.PartialAtomLayout, error)

// Layout reports the old claim layout.
func (uc Claims) Layout(ctx context.Context) (partialentities.PartialAtomLayout, error) {
	folders, claims, indexes, err := uc.AtomStore.DetectLegacyFolders()
	if err != nil {
		return partialentities.PartialAtomLayout{}, err
	}
	return partialentities.PartialAtomLayout{Folders: folders, Claims: claims, Indexes: indexes}, nil
}

// AtomLayoutWriter moves the old claim layout to the flat one. A handler holds this value; nil means
// the feature is not wired.
type AtomLayoutWriter func(ctx context.Context) (partialentities.PartialAtomLayout, error)

// Reorganize moves the old claim layout to the flat one.
func (uc Claims) Reorganize(ctx context.Context) (partialentities.PartialAtomLayout, error) {
	folders, claims, indexes, err := uc.AtomStore.DetectLegacyFolders()
	if err != nil {
		return partialentities.PartialAtomLayout{}, err
	}
	moved, removed, indexesGone, skipped, kept, err := uc.AtomStore.Reorganize(ctx)
	if err != nil {
		return partialentities.PartialAtomLayout{}, err
	}
	// The index learns the new paths FIRST: a path the index still holds from before the move would
	// make a delete miss the file it means to remove.
	for _, a := range moved {
		if err := uc.Atoms.SaveAtom(ctx, a); err != nil && uc.Log != nil {
			uc.Log.Warn("reorganize: index not updated", "atom", a.ID.String(), "path", a.Path, "err", err)
		}
	}
	// A generated atoms/index.md is not written any more (the branch catalog is SQLite's job): a file
	// left by an older build goes, and is counted with the branch indexes that go with the folders.
	if gone, err := uc.AtomStore.RemoveStaleIndex(); err == nil && gone {
		indexesGone++
	} else if err != nil && uc.Log != nil {
		uc.Log.Warn("reorganize: stale index not removed", "err", err)
	}
	out := partialentities.PartialAtomLayout{
		Folders: folders, Claims: claims, Indexes: indexes,
		Moved: len(moved), Removed: removed, IndexesRemoved: indexesGone,
		Skipped: skipped, Kept: kept,
	}
	if uc.Log != nil {
		uc.Log.Info("claims reorganized", "folders", len(folders), "claims", claims,
			"moved", len(moved), "folders_removed", removed, "indexes_removed", indexesGone,
			"skipped", skipped, "kept", kept)
	}
	return out, nil
}
