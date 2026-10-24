package usecases

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// ResetSource throws away everything that was DERIVED from a source and leaves the source
// itself untouched: same id, same bytes, same title and metadata. The tree (rows and sidecar),
// the claims, the pages they compiled and the record of finished parts all go, and the tree
// state returns to `none` — so the next run builds the document up from scratch, exactly as if
// it had just been dropped in.
//
// It is the "start over" button: for testing a pipeline change (a new prompt, a new extractor,
// the boilerplate filter) and for repairing a derivation that an older version produced. Nothing
// is invented and nothing is lost — the bytes are the truth, the derivation is a cache.
type ResetSource struct {
	Index     interfaces.IndexRepository
	Trees     interfaces.TreeStore
	Clean     CleanSource
	SourceLog interfaces.SourceLog
	// Steps clears the derived steps: after a reset, outline and distill are pending again —
	// capture survives, because the source itself does.
	Steps interfaces.StepTracker
	// KeepTitle preserves the source's current title across the reset. A wiki page save writes the
	// new title first and resets the old derivation afterwards; the reset must not put the intake
	// name back over the human's title.
	KeepTitle bool
	Clock     interfaces.Clock
	Log       *slog.Logger
}

// intakeTitle is the title a source had right after intake (step 1). For a file that is its name
// without the extension; for a URL it is the title the PAGE declared — the address is not a title and
// step 1 never made one out of it, so a reset must not turn "The Original Page Title" into
// "article".
func intakeTitle(src entities.Source) string {
	if src.Kind == entities.SourceKindURL {
		return src.Title
	}
	name := strings.TrimSpace(src.OriginalName)
	if name == "" {
		return src.Title
	}
	return strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
}

// ResetResult reports what was thrown away. Pages counts the pages that were MARKED for
// re-derivation (they cited the removed claims); no page is deleted by a reset.
type ResetResult struct {
	Atoms int
	Nodes int
	// Bookmark: a bookmark has no derivation to reset (it is never distilled); its single
	// outline node — what makes it findable — is left alone.
	Bookmark bool
}

// Execute resets one source's derivation.
func (uc ResetSource) Execute(ctx context.Context, hash string) (ResetResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return ResetResult{}, err
	}
	res := ResetResult{Bookmark: src.Bookmark}

	// Claims, the pages that drew on them, and the part records.
	cleaned, err := uc.Clean.Execute(ctx, hash)
	if err != nil {
		return res, err
	}
	res.Atoms = cleaned.Atoms

	if !src.Bookmark {
		// The tree: rows, sidecar, and the state that makes the sweep build it again.
		if nodes, err := uc.Index.ListSourceNodes(ctx, src.ID); err == nil {
			res.Nodes = len(nodes)
		}
		if err := uc.Index.ReplaceSourceNodes(ctx, src.ID, nil); err != nil {
			return res, err
		}
		if err := uc.Trees.Remove(ctx, hash); err != nil && uc.Log != nil {
			uc.Log.Warn("reset: remove tree sidecar", "hash", hash, "err", err)
		}
		if err := uc.Index.SetSourceTree(ctx, hash, string(entities.TreeNone), "", 0); err != nil {
			return res, err
		}
		// The title was derived (the model named the document) and the page count came from
		// the tree: both go back to what an intake leaves behind. A wiki save already wrote the
		// human's title and asks for it to be kept.
		title := intakeTitle(src)
		if uc.KeepTitle {
			title = src.Title
		}
		if err := uc.Index.ResetSourceDerived(ctx, hash, title); err != nil {
			return res, err
		}
	}

	if uc.SourceLog != nil {
		// The log of the derivation that was just thrown away goes with it: after a reset the log
		// tells the story of THIS derivation, starting with the reset itself.
		if err := uc.SourceLog.Remove(ctx, hash); err != nil && uc.Log != nil {
			uc.Log.Warn("reset: clear source log", "hash", hash, "err", err)
		}
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "reset", Level: "warn", Message: "derivation reset",
			Fields: map[string]any{
				"atoms_removed": res.Atoms, "nodes_removed": res.Nodes,
				"bookmark": res.Bookmark, "kept": "source id, bytes, title, metadata",
			},
		})
	}
	if uc.Steps != nil {
		if err := uc.Steps.Reset(hash); err != nil && uc.Log != nil {
			uc.Log.Warn("reset: clear the step record", "hash", hash, "err", err)
		}
	}
	if uc.Log != nil {
		uc.Log.Info("source reset", "hash", hash, "atoms", res.Atoms, "nodes", res.Nodes)
	}
	return res, nil
}
