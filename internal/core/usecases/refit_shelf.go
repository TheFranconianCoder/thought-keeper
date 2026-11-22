package usecases

import (
	"fmt"
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// RefitShelfResult says what one refit changed.
type RefitShelfResult struct {
	Namespace string
	Before    int
	After     int
	// Promoted are the names that earned a slot, Evicted the ones that gave theirs up. A refit that
	// changes nothing says so with two empty lists and zero counts.
	Promoted []string
	Evicted  []string
}

// evictMargin is how much more a candidate must carry than the slot it would take. Without the margin
// a name with three uses would trade places with one that has two on every refit — churn that moves
// claims, costs a retopic pass and buys nothing.
const evictMargin = 2

// RefitShelf re-synthesizes a namespace's shelf from what the corpus carries right now: a slot nobody
// uses is given to a subject the vault actually has, and a subject that grew takes one.
//
// This is the half of the vocabulary's maintenance that makes the shelf FOLLOW the content — the
// derivation files new claims onto the shelf, so without a refit the shelf could only shrink and a new
// subject could never appear. It is deterministic: no model, no gate, no claim rewritten. It moves the
// RECORD, and the corpus follows on the next passes (the claims on an evicted name are off the shelf,
// which is exactly the queue the retopic pass works through). That order is the only one that can
// converge: a shelf that only changed after the claims did would never change at all.
//
// A topic is judged by its share of ALL claims (ShelfLimits), never by its count alone: a catch-all is
// never promoted and gives its slot up, and a thin name neither keeps a slot nor earns one.
//
// The report it is given is the observation the tick already made, so a refit costs one file write and
// no extra read of the claim corpus.
func (uc Vocabulary) RefitShelf(h partialentities.VocabularyHealth, target int) (RefitShelfResult, error) {
	res := RefitShelfResult{Namespace: h.Namespace}
	if uc.Vocab == nil || uc.Clock == nil || target <= 0 {
		return res, nil
	}
	ns := entities.VocabularyNamespace(h.Namespace)
	v, err := uc.Vocab.Read(ns)
	if err != nil {
		return res, err
	}
	if len(v.Shelf) == 0 {
		// No shelf yet: EnsureShelf builds the first one, and it needs no candidate logic.
		return res, nil
	}
	res.Before = len(v.Shelf)

	uses := make(map[string]int, len(h.Observed))
	for _, o := range h.Observed {
		uses[o.Name] = o.Uses
	}
	// A moved-down target trims first, so the promotions below fill the slots that are really free.
	shelf := shelfSizing(append([]string(nil), v.Shelf...), h.Observed, target)

	// The shelf is judged by share first: a catch-all or a thin name gives its slot up, even while the
	// shelf has room. Its claims go off the shelf and the retopic pass re-derives them.
	total := h.Uses
	kept := shelf[:0]
	for _, name := range shelf {
		if uc.Shelf.CatchAll(uses[name], total) || uc.Shelf.Thin(uses[name], total) {
			res.Evicted = append(res.Evicted, name)
			continue
		}
		kept = append(kept, name)
	}
	shelf = kept

	onShelf := make(map[string]bool, len(shelf))
	for _, name := range shelf {
		onShelf[name] = true
	}
	for _, o := range h.Observed {
		// h.Observed is most used first, so the candidates are already in promotion order. A candidate
		// is a subject only inside the share band: a catch-all means everything, a thin name nothing.
		if onShelf[o.Name] {
			continue
		}
		if _, absorbed := v.Absorbed[o.Name]; absorbed {
			continue // an absorbed name IS another name already
		}
		if uc.Shelf.CatchAll(o.Uses, total) || uc.Shelf.Thin(o.Uses, total) {
			continue
		}
		if len(shelf) < target {
			shelf = append(shelf, o.Name)
			onShelf[o.Name] = true
			res.Promoted = append(res.Promoted, o.Name)
			continue
		}
		// Full: the candidate must be worth the coldest slot by a margin, or the shelf would churn.
		idx, coldest := coldestShelfName(shelf, uses)
		if idx < 0 || o.Uses < evictMargin*coldest {
			// The candidates only get smaller from here, so nothing else can qualify either.
			break
		}
		res.Evicted = append(res.Evicted, shelf[idx])
		delete(onShelf, shelf[idx])
		shelf[idx] = o.Name
		onShelf[o.Name] = true
		res.Promoted = append(res.Promoted, o.Name)
	}

	sort.Strings(shelf)
	res.After = len(shelf)
	if len(res.Promoted) == 0 && len(res.Evicted) == 0 && res.Before == res.After {
		return res, nil
	}
	// An evicted name is work again, and its old verdicts no longer hold: the shelf it was judged
	// against is gone. Leaving it in Decided/Relabelled would keep it out of both queues and park its
	// claims off the shelf for good.
	for _, name := range res.Evicted {
		v.Decided = dropName(v.Decided, name)
		v.Relabelled = dropName(v.Relabelled, name)
	}
	v.Shelf = shelf
	v.Target = target
	v.ShelfBuilt = shelfBuildVersion
	v.Updated = uc.Clock.Now()
	if wErr := uc.Vocab.Write(v); wErr != nil {
		return RefitShelfResult{}, fmt.Errorf("write the %s shelf: %w", ns, wErr)
	}
	return res, nil
}

// dropName removes one name from a record's list, keeping the order.
func dropName(names []string, drop string) []string {
	out := names[:0]
	for _, name := range names {
		if name != drop {
			out = append(out, name)
		}
	}
	return out
}

// coldestShelfName is the slot a promotion would take: the shelf name with the fewest uses, and the
// FIRST such name in a stable order, so two refits of an unchanged corpus take the same slot.
func coldestShelfName(shelf []string, uses map[string]int) (int, int) {
	idx, coldest := -1, 0
	for i, name := range shelf {
		n := uses[name]
		if idx < 0 || n < coldest || (n == coldest && name < shelf[idx]) {
			idx, coldest = i, n
		}
	}
	return idx, coldest
}
