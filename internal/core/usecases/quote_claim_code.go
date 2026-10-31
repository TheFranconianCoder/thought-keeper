package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
)

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

// ClaimCodeQuoter wraps bracket syntax in claim bodies in inline code (a one-time repair). A handler
// holds this value; nil means the feature is not wired.
type ClaimCodeQuoter func(ctx context.Context) (QuoteClaimCodeResult, error)

// QuoteCode runs the one-time repair.
func (uc Claims) QuoteCode(ctx context.Context) (QuoteClaimCodeResult, error) {
	candidates, err := uc.Atoms.AtomsWithBracketSyntax(ctx)
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
		a, err := uc.AtomStore.ReadPath(ctx, candidate.Path)
		if err != nil {
			res.Skipped++
			uc.warnQuote("claim not read", candidate, err)
			continue
		}
		body, n := markdown.QuoteBracketSyntax(a.Body)
		if n == 0 {
			continue
		}
		a.Body = body
		a.Path = candidate.Path
		a.UpdatedAt = now
		stored, err := uc.AtomStore.WritePath(ctx, a)
		if err != nil {
			res.Skipped++
			uc.warnQuote("claim not written", candidate, err)
			continue
		}
		// The file is the truth and is written FIRST; the index row follows. A row that cannot be
		// updated is caught by the next reindex, so it is a skip, not a failed repair.
		if err := uc.Atoms.SaveAtom(ctx, stored); err != nil {
			res.Skipped++
			uc.warnQuote("claim not indexed", candidate, err)
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

func (uc Claims) warnQuote(what string, a entities.Atom, err error) {
	if uc.Log != nil {
		uc.Log.Warn("quote claim code: "+what, "atom", a.ID.String(), "path", a.Path, "err", err)
	}
}
