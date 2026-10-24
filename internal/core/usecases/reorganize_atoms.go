package usecases

import (
	"context"
	"log/slog"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// ReorganizeAtoms moves a vault's claims out of the branch folders the OLD layout used and into the
// layout that addresses them by their ID (`atoms/<ab>/<cd>/<id>.md`), the way a source is addressed
// by its hash.
//
// A branch is a name the agent assigns and may revise; the ID is the atom's identity. A file that is
// named after its identity cannot be lost by a rename, and no folder has to be created for a name
// that only exists in the frontmatter — which is why the atoms directory becomes two levels of
// shards, an index and nothing else.
//
// Detection is by NAME: a directory under atoms/ whose name is a known branch is the old layout. The
// action is a button, not a start-up habit: the vault is the user's, and moving their files is
// something they ask for. Both halves are idempotent — a second press finds nothing to move.
type ReorganizeAtoms struct {
	Atoms    interfaces.AtomStore
	AtomRepo interfaces.AtomRepository
	Log      *slog.Logger
}

// Detect reports what the reorganize would move (read-only).
func (uc ReorganizeAtoms) Detect(ctx context.Context) (partialentities.PartialAtomLayout, error) {
	folders, claims, indexes, err := uc.Atoms.DetectLegacyFolders()
	if err != nil {
		return partialentities.PartialAtomLayout{}, err
	}
	return partialentities.PartialAtomLayout{Folders: folders, Claims: claims, Indexes: indexes}, nil
}

// Execute moves the claims, deletes the generated branch indexes and removes the folders that empty
// out.
func (uc ReorganizeAtoms) Execute(ctx context.Context) (partialentities.PartialAtomLayout, error) {
	folders, claims, indexes, err := uc.Atoms.DetectLegacyFolders()
	if err != nil {
		return partialentities.PartialAtomLayout{}, err
	}
	moved, removed, indexesGone, skipped, kept, err := uc.Atoms.Reorganize(ctx)
	if err != nil {
		return partialentities.PartialAtomLayout{}, err
	}
	// The index learns the new paths FIRST: a path the index still holds from before the move would
	// make a delete miss the file it means to remove.
	for _, a := range moved {
		if err := uc.AtomRepo.SaveAtom(ctx, a); err != nil && uc.Log != nil {
			uc.Log.Warn("reorganize: index not updated", "atom", a.ID.String(), "path", a.Path, "err", err)
		}
	}
	dirsLeft, pruneErr := 0, error(nil)
	// A generated atoms/index.md is not written any more (the branch catalog is SQLite's job): a file
	// left by an older build goes, and is counted with the branch indexes that go with the folders.
	if gone, err := uc.Atoms.RemoveStaleIndex(); err == nil && gone {
		indexesGone++
	} else if err != nil && uc.Log != nil {
		uc.Log.Warn("reorganize: stale index not removed", "err", err)
	}
	if pruneErr != nil && uc.Log != nil {
		uc.Log.Warn("reorganize: usage records not pruned", "err", pruneErr)
	}
	out := partialentities.PartialAtomLayout{
		Folders: folders, Claims: claims, Indexes: indexes,
		Moved: len(moved), Removed: removed, IndexesRemoved: indexesGone,
		DirsRemoved: dirsLeft, Skipped: skipped, Kept: kept,
	}
	if uc.Log != nil {
		uc.Log.Info("claims reorganized", "folders", len(folders), "claims", claims,
			"moved", len(moved), "folders_removed", removed, "indexes_removed", indexesGone,
			"dirs_removed", dirsLeft, "skipped", skipped, "kept", kept)
	}
	return out, nil
}
