package usecases

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// The coarsening works in small batches: the shelf plus 600 names is a request a model answers
// reliably, and each answered batch is applied before the next one is asked. A pass over 57,181 names
// is therefore ~90 requests and roughly 0.7 Mio. tokens — resumable, because what was absorbed is on
// disk before the next question starts.
const (
	coarsenBatchSize   = 600
	coarsenMaxBatches  = 96
	coarsenShelfInCall = 700
	coarsenShelfHead   = 200
)

// CoarsenResult is what one coarsening pass did.
type CoarsenResult struct {
	Namespace string
	// Shelf is the size of the target vocabulary after the pass.
	Shelf int
	// Families are the deterministic merges (no model), and Mapped the names the model filed.
	Families int
	Mapped   int
	// Remaining are the names still outside the shelf when the pass stopped (the next press
	// continues them).
	Remaining int
	Tokens    int
	Applied   ApplyVocabularyResult
}

// Coarsen files a namespace's long tail onto its shelf. Two stages, cheapest first:
//
//  1. DETERMINISTIC families — names whose words are the same after the learned broad tokens are
//     removed (the near-duplicate detector already computes them) are merged onto their most used
//     member. No model, no tokens, and it is exactly the part a model cannot do better.
//  2. The MODEL files the rest onto the shelf, in batches, applying each answer before the next
//     question. The shelf is built once from the most used names and then is the fixed target: the
//     vocabulary can no longer grow past it, because the derivation resolves every new name onto it.
func (uc Vocabulary) Coarsen(ctx context.Context, runID ulid.ULID) (res CoarsenResult, err error) {
	res = CoarsenResult{Namespace: string(uc.Namespace)}
	finished := false
	finish := func(status entities.RunStatus, answer string) {
		finished = true
		if uc.Runs == nil {
			return
		}
		run := entities.Run{
			ID: runID, Op: entities.RunVocabulary, Model: uc.Model, Status: status,
			Answer: answer, TotalTokens: res.Tokens, EndedAt: uc.Clock.Now(),
		}
		if status != entities.RunSucceeded {
			run.Error = answer
			run.Answer = ""
		}
		_ = uc.Runs.FinishRun(ctx, run) //nolint:errcheck // the run row is an audit trail; the work already ended
	}
	defer func() {
		if finished {
			return
		}
		if err != nil {
			if deferred, why := windowDeferral(err); deferred {
				finish(entities.RunAborted, why)
				return
			}
			finish(entities.RunFailed, err.Error())
			return
		}
		finish(entities.RunSucceeded, fmt.Sprintf("%s: shelf %d, %d families merged, %d names filed, %d left",
			res.Namespace, res.Shelf, res.Families, res.Mapped, res.Remaining))
	}()
	if uc.Vocab == nil || uc.Clock == nil {
		return res, fmt.Errorf("vocabulary: no store configured")
	}
	healths, err := uc.Report(ctx)
	if err != nil {
		return res, err
	}
	var health partialentities.VocabularyHealth
	found := false
	for _, h := range healths {
		if h.Namespace == string(uc.Namespace) {
			health, found = h, true
			break
		}
	}
	if !found || len(health.Observed) == 0 {
		return res, nil
	}
	observed := health.Observed // most used first

	v, err := uc.Vocab.Read(uc.Namespace)
	if err != nil {
		return res, err
	}

	// Stage 1: the deterministic families. A family whose members already mean the same subject
	// needs no question — and asking would cost a request per family.
	if uc.Apply {
		merged, mErr := uc.mergeFamilies(ctx, health.NearDuplicates, &res)
		res.Families += merged
		if mErr != nil {
			return res, mErr
		}
		if merged > 0 {
			// The catalog changed: re-read what is left before the shelf is built.
			if healths, err = uc.Report(ctx); err != nil {
				return res, err
			}
			for _, h := range healths {
				if h.Namespace == string(uc.Namespace) {
					observed = h.Observed
					break
				}
			}
		}
	}

	// The shelf: the most used names of the current catalog, once. It is a RECORD, so a later pass
	// files onto the same names instead of inventing a new target every time.
	target := uc.Target
	if target <= 0 {
		target = len(observed)
	}
	if len(v.Shelf) == 0 || len(v.Shelf) > target {
		v.Shelf = shelfSizing(v.Shelf, observed, target)
		v.Target = target
	}
	if uc.Clock != nil {
		v.Updated = uc.Clock.Now()
	}
	if wErr := uc.Vocab.Write(v); wErr != nil {
		return res, wErr
	}
	res.Shelf = len(v.Shelf)
	shelfSet := make(map[string]bool, len(v.Shelf))
	shelfIndex := make(map[string][]string, len(v.Shelf)*2)
	for _, name := range v.Shelf {
		shelfSet[name] = true
		for _, token := range uniqueTokens(name) {
			shelfIndex[token] = append(shelfIndex[token], name)
		}
	}

	// What is left to file: everything the vault still uses that is neither on the shelf nor already
	// absorbed. Absorbed is the watermark — it is written after each applied batch.
	pending := make([]string, 0, len(observed))
	for _, o := range observed {
		if shelfSet[o.Name] {
			continue
		}
		if _, absorbed := v.Absorbed[o.Name]; absorbed {
			continue
		}
		pending = append(pending, o.Name)
	}
	res.Remaining = len(pending)
	if len(pending) == 0 {
		return res, nil
	}
	if uc.Gateway == nil || uc.Model == "" || !uc.Apply {
		// The shelf exists, the tail does not have to be filed for the derivation to stop growing:
		// the write resolves new names onto the shelf from now on.
		return res, nil
	}

	head := topObserved(observed, coarsenShelfHead)
	batches := 0
	for start := 0; start < len(pending) && batches < coarsenMaxBatches; start += coarsenBatchSize {
		batch := pending[start:min(start+coarsenBatchSize, len(pending))]
		shelf := shelfForBatch(batch, head, shelfIndex, coarsenShelfInCall)
		if len(shelf) == 0 {
			break
		}
		uc.phase(fmt.Sprintf("filing %d names onto the shelf (%d left)", len(batch), len(pending)-start))
		answer, aErr := uc.askShelf(ctx, shelf, batch)
		if aErr != nil {
			if batches > 0 {
				// Keep what was applied; the next press continues from the watermark.
				break
			}
			return res, aErr
		}
		res.Tokens += answer.tokens
		if len(answer.mapping) > 0 {
			applied, wErr := uc.applyShelf(ctx, answer, v.Shelf)
			if wErr != nil {
				return res, wErr
			}
			res.Mapped += len(applied.Absorbed)
			res.Applied.Atoms += applied.Atoms
			res.Applied.Sources += applied.Sources
			res.Applied.Pages += applied.Pages
			res.Shelf = applied.Canonical
		}
		batches++
		res.Remaining = len(pending) - min(start+coarsenBatchSize, len(pending))
	}
	return res, nil
}

// applyShelf applies one answered batch: the rename, and — when a shelf is named — the shelf as the
// canonical set. It is the same write the curation uses, so a coarsened name is an ordinary alias
// from then on. The shelf passed here is the FULL one: the batch's filtered list exists only to keep
// the prompt small, and writing it back would shrink the vocabulary to those names.
func (uc Vocabulary) applyShelf(ctx context.Context, answer chunkAnswer, shelf []string) (ApplyVocabularyResult, error) {
	rename := uc
	rename.Mapping = answer.mapping
	rename.Canonical = shelf
	apply := func(ctx context.Context) (ApplyVocabularyResult, error) {
		return rename.ApplyMapping(ctx)
	}
	if uc.ApplyGate == nil {
		return apply(ctx)
	}
	var out ApplyVocabularyResult
	err := uc.ApplyGate(ctx, func(ctx context.Context) error {
		got, aErr := apply(ctx)
		out = got
		return aErr
	})
	return out, err
}

// askShelf asks one batch: the shelf, the names, one JSON mapping.
func (uc Vocabulary) askShelf(ctx context.Context, shelf, names []string) (chunkAnswer, error) {
	answer := chunkAnswer{mapping: map[string]string{}}
	callCtx, cancel := context.WithTimeout(ctx, vocabularyCallTimeout)
	defer cancel()
	resp, err := uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
		Model: uc.Model, ReasoningEffort: "minimal",
		Messages: []interfaces.AgentMessage{
			{Role: "system", Content: agents.ShelfSystem},
			{Role: "user", Content: agents.ShelfUser(shelf, names)},
		},
	})
	if err != nil {
		return answer, err
	}
	answer.tokens = resp.TotalTokens
	_, mapping, parseErr := parseVocabularyAnswer(resp.Message.Content)
	if parseErr != nil {
		return answer, fmt.Errorf("vocabulary: the shelf answer was unreadable: %w", parseErr)
	}
	allowed := make(map[string]bool, len(shelf))
	for _, name := range shelf {
		allowed[name] = true
	}
	for from, to := range mapping {
		// A value that is not on the shelf is a hallucination, not a decision: drop it, and the name
		// stays in the tail for the next pass.
		if !allowed[to] || from == to || strings.TrimSpace(from) == "" {
			continue
		}
		answer.mapping[from] = to
	}
	return answer, nil
}

// mergeFamilies merges the deterministic near-duplicate families onto their most used member. It is
// the free half of the coarsening: 1,448 families on the measured vault, without a single token. The
// whole set is applied as ONE rename, so the vocabulary file is rewritten once and the gate is taken
// once.
func (uc Vocabulary) mergeFamilies(ctx context.Context, groups []partialentities.NameGroup, res *CoarsenResult) (int, error) {
	mapping := map[string]string{}
	for _, group := range groups {
		if len(group.Names) < 2 {
			continue
		}
		canonical := group.Names[0] // the report orders a family by uses
		for _, member := range group.Names[1:] {
			if member.Name != canonical.Name {
				mapping[member.Name] = canonical.Name
			}
		}
	}
	if len(mapping) == 0 {
		return 0, nil
	}
	applied, err := uc.applyShelf(ctx, chunkAnswer{mapping: mapping}, nil)
	if err != nil {
		return 0, err
	}
	res.Applied.Atoms += applied.Atoms
	res.Applied.Sources += applied.Sources
	res.Applied.Pages += applied.Pages
	return len(applied.Absorbed), nil
}

// shelfForBatch narrows the shelf to what can matter for one batch: the names that share a word with
// it, plus the most used head. Sending all 3,000 for every 600 names would triple the prompt for no
// decision it does not already have.
func shelfForBatch(batch, head []string, index map[string][]string, limit int) []string {
	seen := make(map[string]bool, limit)
	out := make([]string, 0, limit)
	add := func(name string) {
		if len(out) >= limit || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range head {
		add(name)
	}
	for _, name := range batch {
		for _, token := range uniqueTokens(name) {
			for _, shelfName := range index[token] {
				add(shelfName)
			}
		}
	}
	sort.Strings(out)
	return out
}

// topObserved returns the most used names, capped at limit.
func topObserved(observed []partialentities.NameUses, limit int) []string {
	if limit <= 0 || limit > len(observed) {
		limit = len(observed)
	}
	out := make([]string, 0, limit)
	for _, o := range observed[:limit] {
		out = append(out, o.Name)
	}
	return out
}

// shelfSizing returns the bounded target vocabulary for a namespace: the most used names, trimmed to
// target when there is none yet or the knob moved down. A shelf that already fits is left alone — it
// is a record of the decision, not a cache. The automatic build (EnsureShelf) and the coarsening both
// call it, so the two cannot disagree about what the shelf is.
func shelfSizing(shelf []string, observed []partialentities.NameUses, target int) []string {
	if target <= 0 {
		return shelf
	}
	if len(shelf) == 0 {
		return topObserved(observed, target)
	}
	if len(shelf) > target {
		uses := make(map[string]int, len(observed))
		for _, o := range observed {
			uses[o.Name] = o.Uses
		}
		return topObserved(observedUses(shelf, uses), target)
	}
	return shelf
}

// observedUses turns a name list into the NameUses list topNames expects, keeping its order.
func observedUses(names []string, uses map[string]int) []partialentities.NameUses {
	out := make([]partialentities.NameUses, 0, len(names))
	for _, name := range names {
		out = append(out, partialentities.NameUses{Name: name, Uses: uses[name]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Uses > out[j].Uses })
	return out
}
