package usecases

import (
	"context"
	"fmt"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// EnsureShelfResult says what one look wrote.
type EnsureShelfResult struct {
	// Built names the namespaces whose shelf was written for the first time.
	Built []string
	// Rebuilt names the namespaces whose shelf was built under an older rule (ShelfBuilt <
	// shelfBuildVersion) and is therefore rewritten once from the catalog as it is now.
	Rebuilt []string
	// Trimmed names the namespaces whose shelf was shrunk because the knob moved down.
	Trimmed []string
}

// shelfBuildVersion is the version of the rule a shelf was built under. Version 0 (a file that does
// not carry one) built its names from whatever order the catalog arrived in — and the index answers
// alphabetically, so a measured vault's shelf held the alphabetically first 3,000 names and covered
// 38 % of its claims while the most used 3,000 cover 82 %. Version 2 sorts by uses first and keeps
// only names inside the share band (ADR 0009).
const shelfBuildVersion = 2

// EnsureShelf writes the bounded target vocabulary a vault that predates the shelf does not have yet.
// It is the ONE-TIME migration, and it is deliberately cheap: the most used names of the vault that
// exists, a record in vocabulary/<namespace>.md, no model, no gate, no claim rewrite.
//
// The long tail — the names already in use that are not on the shelf — is filed by the COARSENING,
// which rewrites claims and therefore asks permission. This method only makes the target exist, so
// the derivation stops inventing fine names beside it (the derivation reads the shelf; see
// Derivation).
//
// Only the topics namespace is built here: the tag namespaces' write path reads Canonical and the
// alias table, not a shelf (tag_source.go), so a shelf written for them would be a record nothing
// reads. Their coarsening writes it, and it rewrites Canonical with it.
//
// It is idempotent: a shelf that exists, fits the target and was built under the current rule is left
// alone. A shelf from an older rule is rebuilt once — and `Decided`/`Relabelled` are dropped with it,
// because their verdicts were made against the old names. Keeping them would leave the whole tail
// closed against a shelf it was never asked about.
func (uc Vocabulary) EnsureShelf(ctx context.Context, target func(entities.VocabularyNamespace) int) (EnsureShelfResult, error) {
	res := EnsureShelfResult{}
	if uc.Vocab == nil || uc.Atoms == nil || uc.Clock == nil || target == nil {
		return res, nil
	}
	ns := entities.VocabularyTopics
	want := target(ns)
	if want <= 0 {
		return res, nil
	}
	v, err := uc.Vocab.Read(ns)
	if err != nil {
		return res, err
	}
	if len(v.Shelf) > 0 && len(v.Shelf) <= want && v.ShelfBuilt >= shelfBuildVersion {
		// The record is there, fits and was built under this rule: no observation, no write.
		return res, nil
	}
	observed, err := uc.observed(ctx)
	if err != nil {
		return res, err
	}
	switch {
	case len(observed) == 0:
		return res, nil
	case len(v.Shelf) == 0 && len(observed) <= want:
		// The catalog already fits the target: the catalog IS the shelf, there is no tail to bound.
		return res, nil
	case len(v.Shelf) == 0:
		res.Built = append(res.Built, string(ns))
	case v.ShelfBuilt < shelfBuildVersion:
		res.Rebuilt = append(res.Rebuilt, string(ns))
	default:
		res.Trimmed = append(res.Trimmed, string(ns))
	}
	if v.ShelfBuilt < shelfBuildVersion {
		v.Shelf = buildShelf(observed, want, uc.Shelf)
		// The names were judged against a shelf that was never a target. A verdict about a shelf name
		// is not affected, but every verdict about a TAIL name was made against the wrong names.
		v.Decided = nil
		v.Relabelled = nil
	} else {
		v.Shelf = shelfSizing(v.Shelf, observed, want)
	}
	v.Target = want
	v.ShelfBuilt = shelfBuildVersion
	v.Updated = uc.Clock.Now()
	if wErr := uc.Vocab.Write(v); wErr != nil {
		return EnsureShelfResult{}, fmt.Errorf("write the %s shelf: %w", ns, wErr)
	}
	return res, nil
}

// buildShelf is the bounded target vocabulary from a catalog that is already most-used-first: the
// first `target` names that are inside the share band. The band is what the coarsening may offer and
// what a refit keeps, so building outside it would write slots that are thin from the first day and
// evicted on the next refit. A catalog whose band is empty (a tiny vault, or limits off) falls back
// to the most used names unsliced by share.
func buildShelf(observed []partialentities.NameUses, target int, limits ShelfLimits) []string {
	if target <= 0 || len(observed) == 0 {
		return nil
	}
	total := 0
	for _, o := range observed {
		total += o.Uses
	}
	out := make([]string, 0, min(len(observed), target))
	for _, o := range observed {
		if len(out) >= target {
			break
		}
		if limits.CatchAll(o.Uses, total) || limits.Thin(o.Uses, total) {
			continue
		}
		out = append(out, o.Name)
	}
	if len(out) == 0 {
		return topObserved(observed, target)
	}
	return out
}
