package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/claims"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// RebranchResult says what one pass did.
type RebranchResult struct {
	// Examined is how many claims the pass looked at, Changed how many got a different topic.
	Examined int
	Changed  int
	Batches  int
	Tokens   int
	// Names is how many NAMES the pass answered for (the shelf-aware queue's unit) and Remaining how
	// many are left for the next pass.
	Names     int
	Remaining int
	// Examples names a few before → after, so the reader can judge the labels.
	Examples []string
}

const (
	rebranchPerCall  = 100
	rebranchCallTime = 2 * time.Minute
	// rebranchPage is how many claims of one name are read at a time, and knownInPrompt how many
	// shelf names a question carries. The names a claim is likely to be filed under are the most used
	// ones; handing over the whole catalog was ~2.3 MB per batch.
	rebranchPage  = 200
	knownInPrompt = 400
)

// rebranchAnswer is the model's reply: one topic per claim, addressed by its NUMBER in the question.
type rebranchAnswer struct {
	Claims []struct {
		N     int    `json:"n"`
		Topic string `json:"topic"`
	} `json:"claims"`
}

// labelledBatch is one question and its answer.
type labelledBatch struct {
	atoms  []entities.Atom
	labels map[int]string
	tokens int
	err    error
}

// Rebranch re-derives the topic of the claims the shelf could not take, from the claims' own text.
//
// It is the second half of the reduction to the shelf, and the half that actually OPTIMIZES the
// topics: the coarsening renames a name in bulk (one answer moves every claim of that name), so it can
// only place names that mean one thing. A name whose claims are really several subjects — or one
// subject the shelf names differently — is the unit this pass works in: its claims are asked about one
// batch at a time and every answer resolves onto the shelf.
//
// The queue is what makes it CONVERGE, and it is why it needs no watermark: a name is taken when it is
// outside the shelf and no pass has answered for it yet, most used first. When every batch of a name
// has been attempted the name is recorded in `Relabelled`, so a name that legitimately stays fine is a
// remainder the report shows, never a question that comes back on every cycle.
//
// A vault without a shelf keeps the old repair: the OLDEST claims, one bounded pass after another.
func (uc Claims) Rebranch(ctx context.Context, runID ulid.ULID) (res RebranchResult, err error) {
	// The run row is closed here: a pass that never finishes leaves a run stuck in "running", and a
	// reader would never learn whether it worked.
	defer func() {
		if uc.Runs == nil {
			return
		}
		answer := fmt.Sprintf("retopic: %d name(s), %d claim(s) examined, %d re-labelled (%d question(s), %d tokens, %d left)",
			res.Names, res.Examined, res.Changed, res.Batches, res.Tokens, res.Remaining)
		run := entities.Run{
			ID: runID, Op: entities.RunVocabulary, Model: uc.Model,
			Status: entities.RunSucceeded, Answer: answer, EndedAt: uc.Clock.Now(),
		}
		if err != nil {
			run.Status = entities.RunFailed
			run.Error = err.Error()
			run.Answer = ""
			if deferred, why := windowDeferral(err); deferred {
				// The window cut the batch: the labels this pass wrote are real, the rest waits.
				run.Status = entities.RunAborted
				run.Error = why
			}
		}
		_ = uc.Runs.FinishRun(ctx, run) //nolint:errcheck // the run row is an audit trail; the work already ended
	}()
	if uc.Atoms == nil || uc.Gateway == nil || uc.Model == "" {
		return res, fmt.Errorf("retopic: no model or no atom repository configured")
	}
	settled := make([]string, 0, relabelFlush)
	defer func() { uc.settleRecords(settled) }()
	settle := func(name string) {
		settled = append(settled, name)
		if len(settled) >= relabelFlush {
			uc.settleRecords(settled)
			settled = settled[:0]
		}
	}
	observed, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return res, fmt.Errorf("retopic: read the topics: %w", err)
	}
	v, vErr := uc.Vocab.Read(entities.VocabularyTopics) //nolint:errcheck // a missing record is an empty vocabulary
	if vErr != nil {
		v = entities.Vocabulary{Namespace: entities.VocabularyTopics, Absorbed: map[string]string{}}
	}
	aliases := vocabularyAliases(uc.Vocab, entities.VocabularyTopics)
	catalog := shelfCatalogOf(v.Shelf, observed, uc.TopicShelf)
	started := uc.Clock.Now()
	spent := func() bool {
		return uc.Budget > 0 && uc.Clock.Now().Sub(started) >= uc.Budget
	}
	claimBudget := uc.MaxClaims
	if claimBudget <= 0 {
		claimBudget = 100000
	}

	if len(v.Shelf) == 0 {
		return uc.rebranchOldest(ctx, observed, catalog, aliases, &res)
	}

	queue := retopicQueue(observed, v)
	res.Remaining = len(queue)
	if len(queue) == 0 {
		return res, nil
	}
	known := nameLines(shelfUses(v.Shelf, observed), knownInPrompt)

	// A batch is filled from SEVERAL names. The question does not depend on the name a claim was filed
	// under — the claim's own text decides its topic — so asking one name's seven claims at a time
	// spent a model call per name: 45,000 calls for 341,000 claims. A hundred claims across the queue
	// are the same question with a tenth of the calls. `open` is how many claims of each name have not
	// been asked yet, and a name is settled when its last one has.
	open := make(map[string]int, len(queue))
	buffer := make([]rebranchClaim, 0, rebranchPerCall)
	stop := false
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		batch := append([]rebranchClaim(nil), buffer...)
		buffer = buffer[:0]
		applied := uc.askAndApply(ctx, batch, known, catalog, aliases, spent, &res)
		for _, c := range batch {
			open[c.name]--
			if open[c.name] == 0 {
				settle(c.name)
				res.Names++
				res.Remaining--
			}
		}
		if !applied {
			stop = true
		}
	}

	for _, name := range queue {
		if stop || spent() || res.Examined+len(buffer) >= claimBudget {
			break
		}
		uc.phase(fmt.Sprintf("re-deriving %q (%d left)", name, res.Remaining))
		offset := 0
		for {
			atoms, lErr := uc.Atoms.ListAtoms(ctx, name, rebranchPage, offset)
			if lErr != nil {
				return res, fmt.Errorf("retopic: read the claims of %q: %w", name, lErr)
			}
			if len(atoms) == 0 {
				break
			}
			offset += len(atoms)
			for _, atom := range atoms {
				if res.Examined+len(buffer) >= claimBudget {
					stop = true
					break
				}
				open[name]++
				buffer = append(buffer, rebranchClaim{atom: atom, name: name})
				if len(buffer) >= rebranchPerCall {
					flush()
				}
				if stop {
					break
				}
			}
			if stop || len(atoms) < rebranchPage {
				break
			}
		}
	}
	if !stop {
		flush()
	}
	return res, nil
}

// rebranchClaim is one claim in the queue with the name it was filed under. The name is only what the
// pass settles afterwards: the question itself is the claim's text.
type rebranchClaim struct {
	atom entities.Atom
	name string
}

// askAndApply asks one batch of claims (chunks of a hundred, a few in flight, possibly from several
// names) and applies the answers IN ORDER. It reports whether the pass may continue: everything it
// asked is applied — an answer costs nothing to write — and a spent budget stops the NEXT batch, never
// this one.
func (uc Claims) askAndApply(ctx context.Context, batch []rebranchClaim, known []string, catalog *topicCatalog, aliases map[string]string, spent func() bool, res *RebranchResult) bool {
	if len(batch) == 0 {
		return true
	}
	chunks := make([][]entities.Atom, 0, (len(batch)+rebranchPerCall-1)/rebranchPerCall)
	for from := 0; from < len(batch); from += rebranchPerCall {
		to := min(from+rebranchPerCall, len(batch))
		atoms := make([]entities.Atom, 0, to-from)
		for _, c := range batch[from:to] {
			atoms = append(atoms, c.atom)
		}
		chunks = append(chunks, atoms)
	}
	answers := make([]labelledBatch, len(chunks))
	width := uc.Parallel
	if width <= 0 {
		width = 4
	}
	if width > len(chunks) {
		width = len(chunks)
	}
	slots := make(chan struct{}, width)
	var wg sync.WaitGroup
	for i := range chunks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			answers[i] = uc.ask(ctx, chunks[i], known)
			<-slots
		}(i)
	}
	wg.Wait()
	for i := range answers {
		res.Batches++
		res.Tokens += answers[i].tokens
		res.Examined += len(chunks[i])
		if answers[i].err != nil {
			if uc.Log != nil {
				uc.Log.Warn("retopic: a question failed", "err", answers[i].err)
			}
			continue
		}
		uc.label(ctx, answers[i], catalog, aliases, res)
	}
	return !spent()
}

// settleRecords records that names' claims have been re-derived, so they are not questions again.
// It is called every few names, not once per name: the vocabulary file is megabytes on a grown vault,
// and rewriting it for each of thousands of tail names is gigabytes of I/O for a watermark. A pass that
// dies before the next flush re-asks at most that many names — a cheap loss, and a bounded one.
func (uc Claims) settleRecords(names []string) {
	if uc.Vocab == nil || len(names) == 0 {
		return
	}
	v, err := uc.Vocab.Read(entities.VocabularyTopics)
	if err != nil {
		return
	}
	done := make(map[string]bool, len(v.Relabelled)+len(names))
	for _, name := range v.Relabelled {
		done[name] = true
	}
	added := 0
	for _, name := range names {
		if done[name] {
			continue
		}
		done[name] = true
		v.Relabelled = append(v.Relabelled, name)
		added++
	}
	if added == 0 {
		return
	}
	if uc.Clock != nil {
		v.Updated = uc.Clock.Now()
	}
	if wErr := uc.Vocab.Write(v); wErr != nil && uc.Log != nil {
		uc.Log.Warn("retopic: the relabelled names were not recorded", "names", added, "err", wErr)
	}
}

// relabelFlush is how many settled names are held before the watermark is written.
const relabelFlush = 32

// retopicQueue is the pass's work: the names outside the shelf that no pass has answered for yet, most
// used first. Absorbed names are another name already, and Relabelled names were asked claim by claim —
// asking them again is what would make the loop endless.
func retopicQueue(observed []entities.TopicUse, v entities.Vocabulary) []string {
	onShelf := make(map[string]bool, len(v.Shelf))
	for _, name := range v.Shelf {
		onShelf[name] = true
	}
	done := make(map[string]bool, len(v.Relabelled))
	for _, name := range v.Relabelled {
		done[name] = true
	}
	out := make([]entities.TopicUse, 0, len(observed))
	for _, o := range observed {
		if onShelf[o.Topic] || done[o.Topic] {
			continue
		}
		if _, absorbed := v.Absorbed[o.Topic]; absorbed {
			continue
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Topic < out[j].Topic
	})
	names := make([]string, 0, len(out))
	for _, o := range out {
		names = append(names, o.Topic)
	}
	return names
}

// shelfCatalogOf is the catalog a repair resolves onto: the shelf itself, with no room to grow — the
// point of the pass is to move claims ONTO it. A vault without a shelf gets the observed head, which
// is the behaviour of a vault that never built one.
func shelfCatalogOf(shelf []string, observed []entities.TopicUse, limits ShelfLimits) *topicCatalog {
	if len(shelf) == 0 {
		c := newTopicCatalog(observed)
		c.judgeBy(limits, claimTotal(observed))
		return c
	}
	uses := shelfUses(shelf, observed)
	c := newTopicCatalog(uses)
	c.budget = len(shelf) // no room: an unknown proposal maps onto the best candidate, or is kept
	c.judgeBy(limits, claimTotal(observed))
	return c
}

// shelfUses is the shelf in use order, for the catalog and the prompt.
func shelfUses(shelf []string, observed []entities.TopicUse) []entities.TopicUse {
	counts := make(map[string]int, len(observed))
	for _, o := range observed {
		counts[o.Topic] = o.Count
	}
	uses := make([]entities.TopicUse, 0, len(shelf))
	for _, name := range shelf {
		uses = append(uses, entities.TopicUse{Topic: name, Count: counts[name]})
	}
	sort.SliceStable(uses, func(i, j int) bool {
		if uses[i].Count != uses[j].Count {
			return uses[i].Count > uses[j].Count
		}
		return uses[i].Topic < uses[j].Topic
	})
	return uses
}

// nameLines renders the names a question offers to the model, with their weight.
func nameLines(uses []entities.TopicUse, limit int) []string {
	out := make([]string, 0, min(len(uses), limit))
	for _, u := range uses {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, fmt.Sprintf("%s (%d claims)", u.Topic, u.Count))
	}
	return out
}

// rebranchOldest is the repair for a vault with no shelf: the OLDEST claims, bounded per pass, the
// claims it changed moving to the back of the queue by their new timestamp.
func (uc Claims) rebranchOldest(ctx context.Context, observed []entities.TopicUse, catalog *topicCatalog, aliases map[string]string, res *RebranchResult) (RebranchResult, error) {
	limit := uc.MaxClaims
	if limit <= 0 {
		limit = 100000
	}
	all, err := uc.Atoms.ListOldestAtoms(ctx, limit)
	if err != nil {
		return *res, fmt.Errorf("retopic: read the claims: %w", err)
	}
	if len(all) == 0 {
		return *res, nil
	}
	total := 0
	for _, b := range observed {
		total += b.Count
	}
	if total > len(all) {
		res.Remaining = total - len(all)
	}
	known := nameLines(observed, knownInPrompt)
	uc.phase(fmt.Sprintf("labelling %d claim(s) from the oldest", len(all)))
	noBudget := func() bool { return false }
	batch := make([]rebranchClaim, 0, len(all))
	for _, atom := range all {
		batch = append(batch, rebranchClaim{atom: atom})
	}
	uc.askAndApply(ctx, batch, known, catalog, aliases, noBudget, res)
	return *res, nil
}

// label applies one answered batch: a topic that differs, resolves onto the catalog and passes the
// standalone check is written (file first, then the row).
func (uc Claims) label(ctx context.Context, got labelledBatch, catalog *topicCatalog, aliases map[string]string, res *RebranchResult) {
	for num, name := range got.labels {
		if num < 1 || num > len(got.atoms) {
			continue
		}
		before := got.atoms[num-1]
		next := coremarkdown.NormalizeTopic(name)
		if next == "" {
			continue
		}
		next = catalog.resolve(aliases, next, before.Claim+" "+before.Body).Name
		if next == "" || next == before.Topic {
			continue
		}
		if issues := claims.Issues(next, before.Claim, before.Body); len(issues) > 0 {
			if uc.Log != nil {
				uc.Log.Warn("retopic: topic rejected — the claim is not standalone",
					"atom", before.ID.String(), "topic", next, "reasons", strings.Join(issues, "; "))
			}
			continue
		}
		if !uc.Apply {
			res.Changed++
			continue
		}
		// The queue read carried the TEXT only. A SaveAtom is authoritative for provenance, so the
		// claim is re-read whole before its label moves — writing back the queue row would silently
		// delete what the queue row never held.
		full, err := uc.Atoms.GetAtom(ctx, before.ID)
		if err != nil {
			if uc.Log != nil {
				uc.Log.Warn("retopic: the claim could not be re-read — left as it is",
					"atom", before.ID.String(), "err", err)
			}
			continue
		}
		a := full
		a.Topic = next
		a.UpdatedAt = uc.Clock.Now()
		if err := uc.write(ctx, a); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("retopic: a claim was not re-labelled", "atom", a.ID.String(), "err", err)
			}
			continue
		}
		res.Changed++
		if len(res.Examples) < 12 {
			res.Examples = append(res.Examples,
				fmt.Sprintf("%q: %s → %s", oneLine(a.Claim, 70), before.Topic, next))
		}
	}
}

// ask labels ONE batch: the claims by number, the topics in use as the names to reuse.
func (uc Claims) ask(ctx context.Context, atoms []entities.Atom, known []string) labelledBatch {
	out := labelledBatch{atoms: atoms, labels: map[int]string{}}
	callCtx, cancel := context.WithTimeout(ctx, rebranchCallTime)
	defer cancel()
	resp, err := uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
		Model:           uc.Model,
		ReasoningEffort: "minimal",
		Messages: []interfaces.AgentMessage{
			{Role: "system", Content: agents.RebranchSystem},
			{Role: "user", Content: agents.RebranchUser(known, atomLines(atoms))},
		},
	})
	if err != nil {
		out.err = err
		return out
	}
	out.tokens = resp.TotalTokens
	content := resp.Message.Content
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		out.err = fmt.Errorf("the answer carries no JSON object")
		return out
	}
	var parsed rebranchAnswer
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		out.err = fmt.Errorf("the answer is not valid JSON: %w", err)
		return out
	}
	for _, c := range parsed.Claims {
		out.labels[c.N] = c.Topic
	}
	return out
}

// atomLines renders the batch: number, current topic, claim and body. The body is part of the
// evidence: a topic must be derivable from what the atom says, never from its source.
func atomLines(atoms []entities.Atom) string {
	var b strings.Builder
	for i, a := range atoms {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, a.Topic, oneLine(a.Claim, 200))
		if body := strings.TrimSpace(a.Body); body != "" {
			fmt.Fprintf(&b, "   %s\n", oneLine(body, 220))
		}
	}
	return b.String()
}

// write persists ONE claim: the file first (the source of truth), then the row.
func (uc Claims) write(ctx context.Context, a entities.Atom) error {
	if uc.AtomStore != nil {
		if _, err := uc.AtomStore.Write(ctx, a); err != nil {
			return err
		}
	}
	return uc.Atoms.SaveAtom(ctx, a)
}

func (uc Claims) phase(name string) {
	if uc.OnPhase != nil {
		uc.OnPhase(name)
	}
}
