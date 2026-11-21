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

// The coarsening works in small batches: the shelf plus the batch is one request. The batch was 600
// names until a measured vault showed the model answering six of them; 150 fits the answer well
// inside one completion. Several questions are in flight at once (coarsenParallel), but every
// answered batch is APPLIED in order, one after another: the apply rewrites claims and the count of
// what is left is read from it.
//
// A pass is bounded three ways, and all three are the point of the automatic flow: by names (max
// batches), by a BUDGET (the gate is handed back on purpose instead of being held to the run's
// ceiling), and by what the model actually answers — three questions in a row that come back empty
// or fail mean the shelf cannot be asked any further right now, and the pass stops instead of
// burning the budget. It is resumable, because what was absorbed, filed and judged is on disk before
// the next batch runs.
const (
	coarsenBatchSize   = 150
	coarsenMaxBatches  = 320
	coarsenShelfInCall = 700
	coarsenShelfHead   = 200
	// coarsenFailedAsks is how many questions in a row may fail (an error, or an answer that maps
	// nothing) before the pass stops. An empty answer is NOT a decision: the model said nothing, so
	// the names stay open — settling them would let one bad completion poison the whole tail.
	coarsenFailedAsks = 3
	// coarsenParallel is how many questions are in flight at once. An answer takes minutes and the
	// questions are independent, so this is the one lever that moves the wall clock; the gateway still
	// keeps the process-wide cap (openai.max_concurrent).
	coarsenParallel = 4
	// coarsenVariantChunk bounds one rename of the deterministic variants: one transaction and one
	// read of the claims per chunk.
	coarsenVariantChunk = 2000
	// coarsenSettleVersion is the version of the ASSIGNMENT RULE recorded in the vocabulary
	// (`settle:`). Version 0 — a file that does not carry one — was "a name the shelf cannot honestly
	// cover may be left out", which settled two thirds of a measured tail and left 82 % of its claims
	// on fine labels. Version 2 makes assignment the rule. A pass that finds an older version drops the
	// verdicts of the names OUTSIDE the shelf and asks them again; a verdict about a shelf name is not
	// touched by the rule change.
	coarsenSettleVersion = 2
)

// CoarsenResult is what one coarsening pass did.
type CoarsenResult struct {
	Namespace string
	// Shelf is the size of the target vocabulary after the pass.
	Shelf int
	// Families are the deterministic merges (no model), and Mapped the names the model filed.
	Families int
	Mapped   int
	// Asked is how many names the pass put to the model; Judged what it decided about (filed plus
	// settled), and Remaining how many names the vault still shows outside the shelf, unjudged — the
	// work the next pass continues.
	Asked     int
	Judged    int
	Remaining int
	Tokens    int
	Applied   ApplyVocabularyResult
}

// Coarsen files a namespace's long tail onto its shelf. Two stages, cheapest first:
//
//  1. DETERMINISTIC families — names whose words are the same after the learned broad tokens are
//     removed (the near-duplicate detector already computes them) are merged onto their most used
//     member. No model, no tokens, and it is exactly the part a model cannot do better.
//  2. The MODEL files the rest onto the shelf, in batches. The shelf is built once from the most
//     used names and then is the fixed target: the vocabulary can no longer grow past it, because
//     the derivation resolves every new name onto it.
//
// The pass is meant to be REPEATED: the app asks for the next one when this one ends (the tail is
// resumable from Absorbed and Decided), a budget hands the gate back, and a name the model passed
// over is a decision recorded in Decided — so the tail is asked about once and the count of what is
// left converges instead of describing the same forty thousand names every hour.
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
		// The families wrote the vocabulary themselves, through the same rename a curation uses. The
		// copy read above predates every alias they recorded, so it is re-read BEFORE anything writes
		// it back: writing the stale copy dropped the whole deterministic stage from the record.
		if v, err = uc.Vocab.Read(uc.Namespace); err != nil {
			return res, err
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
	// files onto the same names instead of inventing a new target every time. A shelf that fits is
	// left alone, and an unchanged record is not written again.
	target := uc.Target
	if target <= 0 {
		target = len(observed)
	}
	if len(v.Shelf) == 0 || len(v.Shelf) > target {
		v.Shelf = shelfSizing(v.Shelf, observed, target)
		v.Target = target
		if uc.Clock != nil {
			v.Updated = uc.Clock.Now()
		}
		if wErr := uc.Vocab.Write(v); wErr != nil {
			return res, wErr
		}
	}
	res.Shelf = len(v.Shelf)
	shelfSet := make(map[string]bool, len(v.Shelf))
	for _, name := range v.Shelf {
		shelfSet[name] = true
	}
	// A claim is filed onto a shelf name only when that name is in band (ADR 0009): a catch-all would
	// take every claim it is renamed onto, and a thin name is a slot nobody uses. Membership above still
	// counts every shelf name, so an out-of-band name is never mistaken for a tail name.
	offerable := inBandNames(v.Shelf, observed, health.Uses, uc.Shelf)
	offerableSet := make(map[string]bool, len(offerable))
	shelfIndex := make(map[string][]string, len(offerable)*2)
	for _, name := range offerable {
		offerableSet[name] = true
		for _, token := range uniqueTokens(name) {
			shelfIndex[token] = append(shelfIndex[token], name)
		}
	}

	// The assignment rule moved: the names an older rule settled as unfilable are questions again.
	if v.Settle != coarsenSettleVersion {
		kept := make([]string, 0, len(v.Decided))
		for _, name := range v.Decided {
			if shelfSet[name] {
				kept = append(kept, name)
			}
		}
		dropped := len(v.Decided) - len(kept)
		v.Decided = kept
		v.Settle = coarsenSettleVersion
		if uc.Clock != nil {
			v.Updated = uc.Clock.Now()
		}
		if wErr := uc.Vocab.Write(v); wErr != nil {
			return res, wErr
		}
		if uc.Log != nil && dropped > 0 {
			uc.Log.Info("vocabulary: a new assignment rule re-opens the settled tail",
				"namespace", string(uc.Namespace), "names", dropped)
		}
	}

	// What is left to file: everything the vault still uses that is neither on the shelf, nor already
	// absorbed, nor already judged. Absorbed and Decided are the watermark — both are written after
	// each applied batch, and a name the model passed over counts as judged (the shelf cannot honestly
	// cover it), so a pass over the tail is finite and the next one continues further in.
	judgedSet := make(map[string]bool, len(v.Decided))
	for _, name := range v.Decided {
		judgedSet[name] = true
	}
	pending := make([]string, 0, len(observed))
	for _, o := range observed {
		if shelfSet[o.Name] {
			continue
		}
		if _, absorbed := v.Absorbed[o.Name]; absorbed {
			continue
		}
		if judgedSet[o.Name] {
			continue
		}
		pending = append(pending, o.Name)
	}
	res.Remaining = len(pending)
	if len(pending) == 0 {
		return res, nil
	}
	if !uc.Apply {
		return res, nil
	}

	// Stage 2a: the VARIANTS. A tail name that is a shelf name plus a modifier at either end
	// ("sample-size-insensitivity", "go-code-generation") is filed here, with no model call and no
	// guessing about the subject: a token overlap would happily file "moral-psychology" onto
	// "evolutionary-psychology", and this rule does not.
	if len(shelfSet) > 0 {
		mapping := make(map[string]string, len(pending)/4)
		for _, name := range pending {
			if to, ok := shelfVariant(name, offerableSet); ok {
				mapping[name] = to
			}
		}
		if len(mapping) > 0 {
			names := make([]string, 0, len(mapping))
			for from := range mapping {
				names = append(names, from)
			}
			// What actually went through: the apply drops a structural target, so the names to take
			// out of the queue are its answer, not the mapping that was proposed.
			filed := make(map[string]bool, len(mapping))
			for start := 0; start < len(names); start += coarsenVariantChunk {
				end := min(start+coarsenVariantChunk, len(names))
				chunk := make(map[string]string, end-start)
				for _, from := range names[start:end] {
					chunk[from] = mapping[from]
				}
				applied, wErr := uc.applyShelf(ctx, chunkAnswer{mapping: chunk}, v.Shelf, nil)
				if wErr != nil {
					return res, wErr
				}
				for from := range applied.Absorbed {
					filed[from] = true
				}
				res.Applied.Atoms += applied.Atoms
				res.Applied.Sources += applied.Sources
				res.Applied.Pages += applied.Pages
				res.Shelf = applied.Canonical
			}
			res.Mapped += len(filed)
			left := pending[:0]
			for _, name := range pending {
				if filed[name] {
					continue
				}
				left = append(left, name)
			}
			pending = left
			res.Remaining = len(pending)
			if uc.Log != nil {
				uc.Log.Info("vocabulary: variants filed", "namespace", string(uc.Namespace),
					"names", len(filed), "left", res.Remaining)
			}
		}
	}
	if len(pending) == 0 {
		return res, nil
	}
	if uc.Gateway == nil || uc.Model == "" {
		// The shelf exists, the tail does not have to be filed for the derivation to stop growing:
		// the write resolves new names onto the shelf from now on.
		return res, nil
	}

	head := keepNames(topObserved(observed, coarsenShelfHead), offerableSet)

	// The questions are independent and one answer takes minutes, so several are in flight at once.
	// The APPLY stays in order, one batch after another: it rewrites claims, and `Remaining` is its
	// count. The gateway keeps the process-wide cap (openai.max_concurrent), so this is latency, not
	// an unbounded burst.
	type shelfJob struct {
		batch []string
		shelf []string
	}
	jobs := make([]shelfJob, 0, coarsenMaxBatches)
	for start := 0; start < len(pending) && len(jobs) < coarsenMaxBatches; start += coarsenBatchSize {
		batch := pending[start:min(start+coarsenBatchSize, len(pending))]
		shelf := shelfForBatch(batch, head, shelfIndex, coarsenShelfInCall)
		if len(shelf) == 0 {
			break
		}
		jobs = append(jobs, shelfJob{batch: batch, shelf: shelf})
	}
	if len(jobs) == 0 {
		return res, nil
	}
	parallel := uc.Parallel
	if parallel <= 0 {
		parallel = coarsenParallel
	}
	if parallel > len(jobs) {
		parallel = len(jobs)
	}

	// The BUDGET is how a pass hands the gate back on purpose. An automatic pass is followed by
	// another one, so it does not have to run to the run's ceiling: it stops LAUNCHING once the budget
	// is spent, applies the answers already in flight and returns, and the next pass continues from
	// the watermark. Without it the ceiling ends the pass in the middle of a question and throws those
	// answers away.
	started := uc.Clock.Now()
	budgetSpent := func() bool {
		return uc.Budget > 0 && uc.Clock.Now().Sub(started) >= uc.Budget
	}

	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type shelfResult struct {
		index  int
		answer chunkAnswer
		err    error
	}
	// A worker can answer after we stopped reading, so the channel holds one slot per job: no worker
	// ever blocks on the send, and none is left behind.
	results := make(chan shelfResult, len(jobs))
	next := 0
	launch := func() {
		job, index := jobs[next], next
		next++
		uc.phase(fmt.Sprintf("filing %d names onto the shelf (%d left)", len(job.batch), res.Remaining))
		go func() {
			answer, err := uc.askShelf(askCtx, job.shelf, job.batch)
			results <- shelfResult{index: index, answer: answer, err: err}
		}()
	}
	for next < parallel {
		launch()
	}

	arrived := make(map[int]shelfResult, parallel)
	batches, failed := 0, 0
	// stopAt is how far this pass applies: when the budget is spent it moves down to the questions
	// already in flight, so nothing launched is thrown away.
	stopAt := len(jobs)
	for index := 0; index < stopAt; index++ {
		var got shelfResult
		if early, ok := arrived[index]; ok {
			got = early
			delete(arrived, index)
		} else {
			for {
				from := <-results
				if from.index == index {
					got = from
					break
				}
				arrived[from.index] = from
			}
		}
		if next < len(jobs) {
			if budgetSpent() {
				stopAt = next
			} else {
				launch()
			}
		}
		if got.err != nil {
			if deferred, _ := windowDeferral(got.err); deferred {
				cancel()
				if batches > 0 {
					// Keep what was applied; the window's wake asks for the next pass.
					break
				}
				return res, got.err
			}
			failed++
			if uc.Log != nil {
				uc.Log.Warn("vocabulary: a shelf question failed — its names stay open",
					"namespace", string(uc.Namespace), "asked", len(jobs[index].batch),
					"failures", failed, "err", got.err)
			}
			if failed >= coarsenFailedAsks {
				cancel()
				break
			}
			continue
		}
		res.Asked += len(jobs[index].batch)
		res.Tokens += got.answer.tokens
		// An answer that maps nothing is not a judgement: the model said nothing, so its names stay
		// open for the next pass instead of being settled by a completion nobody can trust.
		if len(got.answer.mapping) == 0 {
			failed++
			if uc.Log != nil {
				uc.Log.Warn("vocabulary: the shelf answer mapped nothing",
					"namespace", string(uc.Namespace), "asked", len(jobs[index].batch),
					"failures", failed, "tokens", got.answer.tokens)
			}
			if failed >= coarsenFailedAsks {
				cancel()
				break
			}
			continue
		}
		failed = 0
		// Every asked name the model did NOT file is judged and kept: the shelf cannot honestly cover
		// it. That decision is what makes a pass over the tail finite — the next pass does not ask
		// about it again.
		judged := unfiledNames(jobs[index].batch, got.answer.mapping)
		applied, wErr := uc.applyShelf(ctx, got.answer, v.Shelf, judged)
		if wErr != nil {
			cancel()
			return res, wErr
		}
		filed := len(applied.Absorbed)
		res.Mapped += filed
		res.Judged += len(judged)
		res.Applied.Atoms += applied.Atoms
		res.Applied.Sources += applied.Sources
		res.Applied.Pages += applied.Pages
		res.Shelf = applied.Canonical
		batches++
		// The number LEFT is what the vault still shows outside the shelf, not a count of the batches
		// walked: the two differed by everything the model declined to file.
		res.Remaining -= filed + len(judged)
		if uc.Log != nil {
			// THE measurement of this pass: how many of the names asked were filed and how many were
			// settled. A batch that files nothing but settles its names has still moved the tail.
			uc.Log.Info("vocabulary: shelf batch", "namespace", string(uc.Namespace),
				"asked", len(jobs[index].batch), "filed", filed, "judged", len(judged),
				"left", res.Remaining, "tokens", got.answer.tokens, "parallel", parallel)
		}
	}
	return res, nil
}

// unfiledNames is what one answer did not file: the names it judged and left outside the shelf.
func unfiledNames(batch []string, mapping map[string]string) []string {
	out := make([]string, 0, len(batch))
	for _, name := range batch {
		if _, filed := mapping[name]; filed {
			continue
		}
		out = append(out, name)
	}
	return out
}

// shelfVariant reports the shelf name a tail name is a VARIANT of: a contiguous phrase of the name,
// the same words in the same order, carrying at least two words. It is the cheap half of the
// coarsening after the near-duplicate families, and it is deliberately literal: "sample-size-
// insensitivity" is sample-size, "go-code-generation" is code-generation and "string-to-floating-
// point-conversion" is floating-point, while "moral-psychology" is NOT "evolutionary-psychology" —
// a shared subject word never qualifies, because the shelf name has to BE a phrase of the name.
func shelfVariant(name string, shelf map[string]bool) (string, bool) {
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return "", false // a one- or two-word name is not a variant of a two-word shelf name
	}
	// Longest phrase first: "decimal-floating-point-rounding" is decimal-floating-point before it is
	// floating-point.
	for k := len(parts) - 1; k >= 2; k-- {
		for start := 0; start+k <= len(parts); start++ {
			if cand := strings.Join(parts[start:start+k], "-"); shelf[cand] {
				return cand, true
			}
		}
	}
	return "", false
}

// applyShelf applies one answered batch: the rename, the shelf as the canonical set (when a shelf is
// named), and the names the answer did not file as judged. It is the same write the curation uses, so
// a coarsened name is an ordinary alias from then on. The shelf passed here is the FULL one: the
// batch's filtered list exists only to keep the prompt small, and writing it back would shrink the
// vocabulary to those names.
func (uc Vocabulary) applyShelf(ctx context.Context, answer chunkAnswer, shelf []string, judged []string) (ApplyVocabularyResult, error) {
	rename := uc
	rename.Mapping = answer.mapping
	rename.Canonical = shelf
	rename.Judged = judged
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
	asked := make(map[string]bool, len(names))
	for _, name := range names {
		asked[name] = true
	}
	for from, to := range mapping {
		// A value that is not on the shelf is a hallucination, not a decision, and a name that was
		// not asked cannot be filed here: drop both, and the name stays in the tail for the next
		// pass. The count of what was ASKED and what was filed is the measurement of the pass.
		if !asked[from] || !allowed[to] || from == to || strings.TrimSpace(from) == "" {
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
	applied, err := uc.applyShelf(ctx, chunkAnswer{mapping: mapping}, nil, nil)
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

// inBandNames returns the shelf names a claim may be filed under: those whose share of all claims is
// inside the band. Names the vault does not use at all have no share and are thin.
func inBandNames(shelf []string, observed []partialentities.NameUses, total int, limits ShelfLimits) []string {
	uses := make(map[string]int, len(observed))
	for _, o := range observed {
		uses[o.Name] = o.Uses
	}
	out := make([]string, 0, len(shelf))
	for _, name := range shelf {
		if limits.CatchAll(uses[name], total) || limits.Thin(uses[name], total) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// keepNames keeps the names that are in the set, in their order.
func keepNames(names []string, keep map[string]bool) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if keep[name] {
			out = append(out, name)
		}
	}
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
