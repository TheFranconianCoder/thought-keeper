package usecases

import (
	"context"
	"fmt"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// EnsureShelfResult says what one look wrote.
type EnsureShelfResult struct {
	// Built names the namespaces whose shelf was written for the first time.
	Built []string
	// Trimmed names the namespaces whose shelf was shrunk because the knob moved down.
	Trimmed []string
}

// EnsureShelf writes the bounded target vocabulary a vault that predates the shelf does not have yet.
// It is the ONE-TIME migration, and it is deliberately cheap: the most used names of the vault that
// exists, a record in vocabulary/<namespace>.md, no model, no gate, no claim rewrite.
//
// The long tail — the names already in use that are not on the shelf — is filed by the COARSENING,
// which rewrites claims and therefore asks permission. This method only makes the target exist, so the
// derivation stops inventing fine names beside it (the derivation reads the shelf; see Derivation).
//
// Only the topics namespace is built here: the tag namespaces' write path reads Canonical and the
// alias table, not a shelf (tag_source.go), so a shelf written for them would be a record nothing
// reads. Their coarsening writes it, and it rewrites Canonical with it.
//
// It is idempotent: a shelf that exists and fits the target is left alone, and a shelf that does not
// fit is trimmed to the most used names.
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
	if len(v.Shelf) > 0 && len(v.Shelf) <= want {
		// The record is there and fits: no observation, no write. This is the steady state.
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
	default:
		res.Trimmed = append(res.Trimmed, string(ns))
	}
	v.Shelf = shelfSizing(v.Shelf, observed, want)
	v.Target = want
	v.Updated = uc.Clock.Now()
	if wErr := uc.Vocab.Write(v); wErr != nil {
		return EnsureShelfResult{}, fmt.Errorf("write the %s shelf: %w", ns, wErr)
	}
	return res, nil
}
