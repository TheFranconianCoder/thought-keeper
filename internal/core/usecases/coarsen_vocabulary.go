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

// The coarsening works in small batches: the shelf plus the batch is one request, and each answered
// batch is applied before the next one is asked. The batch was 600 names until a measured vault
// showed the model answering six of them; 150 fits the answer well inside one completion. A press is
// bounded by names (max batches) and by what the model actually files: three batches in a row that
// file nothing mean the shelf cannot honestly cover the tail, and the pass stops instead of asking
// for hours. It is resumable, because what was absorbed is on disk before the next question starts.
const (
	coarsenBatchSize   = 150
	coarsenMaxBatches  = 320
	coarsenShelfInCall = 700
	coarsenShelfHead   = 200
	// coarsenEmptyBatches is how many batches in a row may file nothing before the pass stops.
	coarsenEmptyBatches = 3
	// coarsenParallel is how many questions are in flight at once. An answer takes minutes and the
	// questions are independent, so this is the one lever that moves the wall clock; the gateway still
	// keeps the process-wide cap (openai.max_concurrent).
	coarsenParallel = 4
	// coarsenVariantChunk bounds one rename of the deterministic variants: one transaction and one
	// read of the claims per chunk.
	coarsenVariantChunk = 2000
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
			if to, ok := shelfVariant(name, shelfSet); ok {
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
				applied, wErr := uc.applyShelf(ctx, chunkAnswer{mapping: chunk}, v.Shelf)
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

	head := topObserved(observed, coarsenShelfHead)

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
	batches := 0
	empty := 0
	for index := 0; index < len(jobs); index++ {
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
			launch()
		}
		if got.err != nil {
			cancel()
			if batches > 0 {
				// Keep what was applied; the next press continues from the watermark.
				break
			}
			return res, got.err
		}
		res.Tokens += got.answer.tokens
		filed := 0
		if len(got.answer.mapping) > 0 {
			applied, wErr := uc.applyShelf(ctx, got.answer, v.Shelf)
			if wErr != nil {
				cancel()
				return res, wErr
			}
			filed = len(applied.Absorbed)
			res.Mapped += filed
			res.Applied.Atoms += applied.Atoms
			res.Applied.Sources += applied.Sources
			res.Applied.Pages += applied.Pages
			res.Shelf = applied.Canonical
		}
		batches++
		// The number LEFT is what the vault still shows outside the shelf, not a count of the batches
		// walked: the two differed by everything the model declined to file.
		res.Remaining -= filed
		if uc.Log != nil {
			// THE measurement of this pass: how many of the names asked were actually filed. A batch
			// that files nothing is the model saying the shelf cannot honestly cover those names.
			uc.Log.Info("vocabulary: shelf batch", "namespace", string(uc.Namespace),
				"asked", len(jobs[index].batch), "filed", filed, "left", res.Remaining,
				"tokens", got.answer.tokens, "parallel", parallel)
		}
		if filed == 0 {
			empty++
			if empty >= coarsenEmptyBatches {
				if uc.Log != nil {
					uc.Log.Warn("vocabulary: the shelf cannot file what is left", "namespace", string(uc.Namespace),
						"left", res.Remaining)
				}
				cancel()
				break
			}
			continue
		}
		empty = 0
	}
	return res, nil
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
