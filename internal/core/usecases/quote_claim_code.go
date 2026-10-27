package usecases

import (
	"context"
	"log/slog"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// QuoteClaimCode wraps bracket syntax in claim bodies in an inline code mark. A claim has no links:
// a `[[...]]` span in its body is syntax quoted from a source (a C attribute, a shell test, a
// grammar fragment), and it must read as code, not as a reference. The pass is a one-time repair
// for a vault written while bodies were still parsed for links; it is idempotent.
//
// It touches only what the index says still carries a bracket pair, and it reads each file before it
// writes: a body that changed after the row was indexed costs a skip, never a wrong rewrite. Claim
// text, topic, provenance and type are not touched.
type QuoteClaimCode struct {
	Atoms    interfaces.AtomStore
	AtomRepo interfaces.AtomRepository
	Clock    interfaces.Clock
	Log      *slog.Logger
}

// QuoteClaimCodeResult reports what the repair did.
type QuoteClaimCodeResult struct {
	// Scanned is how many candidate claims the index named.
	Scanned int
	// Repaired is how many claim bodies changed.
	Repaired int
	// Fragments is how many bracket spans were wrapped.
	Fragments int
	// Skipped is how many claims could not be read or written back.
	Skipped int
}

// Execute repairs the candidates and finishes.
func (uc QuoteClaimCode) Execute(ctx context.Context) (QuoteClaimCodeResult, error) {
	candidates, err := uc.AtomRepo.AtomsWithBracketSyntax(ctx)
	if err != nil {
		return QuoteClaimCodeResult{}, err
	}
	var res QuoteClaimCodeResult
	res.Scanned = len(candidates)
	if len(candidates) == 0 {
		return res, nil
	}
	now := uc.Clock.Now().UTC()
	for _, candidate := range candidates {
		a, err := uc.Atoms.ReadPath(ctx, candidate.Path)
		if err != nil {
			res.Skipped++
			uc.warn("claim not read", candidate, err)
			continue
		}
		body, n := markdown.QuoteBracketSyntax(a.Body)
		if n == 0 {
			continue
		}
		a.Body = body
		a.Path = candidate.Path
		a.UpdatedAt = now
		stored, err := uc.Atoms.WritePath(ctx, a)
		if err != nil {
			res.Skipped++
			uc.warn("claim not written", candidate, err)
			continue
		}
		// The file is the truth and is written FIRST; the index row follows. A row that cannot be
		// updated is caught by the next reindex, so it is a skip, not a failed repair.
		if err := uc.AtomRepo.SaveAtom(ctx, stored); err != nil {
			res.Skipped++
			uc.warn("claim not indexed", candidate, err)
			continue
		}
		res.Repaired++
		res.Fragments += n
	}
	if uc.Log != nil {
		uc.Log.Info("claim code quoted", "scanned", res.Scanned, "repaired", res.Repaired,
			"fragments", res.Fragments, "skipped", res.Skipped)
	}
	return res, nil
}

func (uc QuoteClaimCode) warn(what string, a entities.Atom, err error) {
	if uc.Log != nil {
		uc.Log.Warn("quote claim code: "+what, "atom", a.ID.String(), "path", a.Path, "err", err)
	}
}
