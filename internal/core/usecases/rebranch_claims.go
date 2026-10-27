package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// RebranchClaims re-derives the TOPIC of every claim from the claim itself.
//
// A topic is the subject of ONE claim — the finest level of the name vocabularies. It is written by the
// distill, per claim, and nothing else can recover it: when a curation collapsed the branch vocabulary
// to a handful of umbrella names (it did — 104 names merged under a numeric aim), every claim lost the
// subject it was derived with, and an alias table cannot split a merge back: it does not know which
// claim had which name. The claim TEXT is the evidence that survives, so the labels are re-derived from
// it — the model gets the claims and the topics in use, and names each claim's subject.
//
// It is a LABEL change and nothing else. No claim is rewritten and no source is read.
// The claims are taken OLDEST FIRST and a re-labelled claim gets a new `updated_at`, so the next run
// continues where this one stopped — the queue is the clock, not a cursor file.
type RebranchClaims struct {
	Atoms     interfaces.AtomRepository
	AtomStore interfaces.AtomStore
	Vocab     interfaces.VocabularyStore
	Gateway   interfaces.AgentGateway
	Model     string
	Runs      interfaces.RunRepository
	Clock     interfaces.Clock
	Log       *slog.Logger
	OnPhase   func(phase string)
	// MaxClaims bounds one run. The corpus of a grown vault is tens of thousands of claims; a pass that
	// runs for hours is a pass nobody starts. The rest follows on the next press (oldest first).
	MaxClaims int
	// Parallel is how many questions are in flight. They write nothing; the labels are applied after.
	Parallel int
	// Apply=false only measures (a dry run).
	Apply bool
}

// RebranchResult says what one pass did.
type RebranchResult struct {
	// Examined is how many claims the pass looked at, Changed how many got a different branch.
	Examined int
	Changed  int
	Batches  int
	Tokens   int
	// Remaining estimates what is left for the next press.
	Remaining int
	// Examples names a few before → after, so the reader can judge the labels.
	Examples []string
}

const (
	rebranchPerCall  = 100
	rebranchCallTime = 2 * time.Minute
)

// rebranchAnswer is the model's reply: one branch per claim, addressed by its NUMBER in the question.
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

// Execute re-labels one bounded batch of claims.
func (uc RebranchClaims) Execute(ctx context.Context, runID ulid.ULID) (res RebranchResult, err error) {
	// The run row is closed here: a pass that never finishes leaves a run stuck in "running", and a
	// reader who pressed the button would never learn whether it worked.
	defer func() {
		if uc.Runs == nil {
			return
		}
		answer := fmt.Sprintf("rebranch: %d examined, %d re-labelled (%d question(s), %d tokens, %d left for the next pass)",
			res.Examined, res.Changed, res.Batches, res.Tokens, res.Remaining)
		run := entities.Run{
			ID: runID, Op: entities.RunVocabulary, Model: uc.Model,
			Status: entities.RunSucceeded, Answer: answer, EndedAt: uc.Clock.Now(),
		}
		if err != nil {
			run.Status = entities.RunFailed
			run.Error = err.Error()
			run.Answer = ""
		}
		_ = uc.Runs.FinishRun(ctx, run)
	}()
	if uc.Atoms == nil || uc.Gateway == nil || uc.Model == "" {
		return res, fmt.Errorf("rebranch: no model or no atom repository configured")
	}
	// OLDEST FIRST, and only what this pass can handle: the index answers the queue order, so a
	// budget of 12k claims reads 12k rows. The claims this pass CHANGES move to the back of the queue
	// by their new timestamp, so the next pass continues where this one stopped — the queue is the
	// clock, not a cursor file. (The old shape read the newest 100k claims, sorted them in memory and
	// cut the oldest budget off the front — in a vault past 100k claims, that list could never even
	// SEE the oldest claims.)
	limit := uc.MaxClaims
	if limit <= 0 {
		limit = 100000
	}
	all, err := uc.Atoms.ListOldestAtoms(ctx, limit)
	if err != nil {
		return res, fmt.Errorf("rebranch: read the claims: %w", err)
	}
	if len(all) == 0 {
		return res, nil
	}
	res.Examined = len(all)

	branches, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return res, fmt.Errorf("rebranch: read the branches: %w", err)
	}
	// The catalog's counts are the corpus: what is left for the next pass is the honest number the
	// bounded read could not know by itself.
	total := 0
	for _, b := range branches {
		total += b.Count
	}
	if total > len(all) {
		res.Remaining = total - len(all)
	}
	known := make([]string, 0, len(branches))
	for _, b := range branches {
		known = append(known, fmt.Sprintf("- %s (%d claims)", b.Topic, b.Count))
	}
	sort.Strings(known)

	batches := make([][]entities.Atom, 0, (len(all)+rebranchPerCall-1)/rebranchPerCall)
	for from := 0; from < len(all); from += rebranchPerCall {
		to := min(from+rebranchPerCall, len(all))
		batches = append(batches, all[from:to])
	}
	res.Batches = len(batches)
	uc.phase(fmt.Sprintf("labelling %d claim(s) in %d question(s)", len(all), len(batches)))

	// ASK first (bounded in flight), then apply in order: a question writes nothing.
	answers := make([]labelledBatch, len(batches))
	width := uc.Parallel
	if width <= 0 {
		width = 4
	}
	slots := make(chan struct{}, width)
	var wg sync.WaitGroup
	var done counter
	for i := range batches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			answers[i] = uc.ask(ctx, batches[i], known)
			uc.phase(fmt.Sprintf("labelling (%d/%d answered)", done.add(1), len(batches)))
		}(i)
	}
	wg.Wait()

	for i := range answers {
		res.Tokens += answers[i].tokens
		if answers[i].err != nil {
			if uc.Log != nil {
				uc.Log.Warn("rebranch: a question failed", "batch", i+1, "of", len(batches), "err", answers[i].err)
			}
			continue
		}
		for num, name := range answers[i].labels {
			if num < 1 || num > len(answers[i].atoms) {
				continue
			}
			before := answers[i].atoms[num-1]
			next := coremarkdown.NormalizeTopic(name)
			if next == "" || next == before.Topic {
				continue
			}
			if issues := claims.Issues(next, before.Claim, before.Body); len(issues) > 0 {
				if uc.Log != nil {
					uc.Log.Warn("rebranch: topic rejected — the claim is not standalone",
						"atom", before.ID.String(), "topic", next, "reasons", strings.Join(issues, "; "))
				}
				continue
			}
			if !uc.Apply {
				res.Changed++
				continue
			}
			// The queue read carried the TEXT only. A SaveAtom is authoritative for provenance, so
			// the claim is re-read whole before its label moves — writing back the queue row would
			// silently delete what the queue row never held.
			full, err := uc.Atoms.GetAtom(ctx, before.ID)
			if err != nil {
				if uc.Log != nil {
					uc.Log.Warn("rebranch: the claim could not be re-read — left as it is",
						"atom", before.ID.String(), "err", err)
				}
				continue
			}
			a := full
			a.Topic = next
			a.UpdatedAt = uc.Clock.Now()
			if err := uc.write(ctx, a); err != nil {
				if uc.Log != nil {
					uc.Log.Warn("rebranch: a claim was not re-labelled", "atom", a.ID.String(), "err", err)
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
	if uc.Apply && res.Changed > 0 {
		uc.rebuildVocabulary(ctx)
	}
	if uc.Log != nil {
		uc.Log.Info("rebranch: claims re-labelled", "run", runID.String(),
			"examined", res.Examined, "changed", res.Changed, "batches", res.Batches,
			"tokens", res.Tokens, "remaining", res.Remaining, "dry_run", !uc.Apply)
	}
	return res, nil
}

// ask labels ONE batch: the claims by number, the topics in use as the names to reuse.
func (uc RebranchClaims) ask(ctx context.Context, atoms []entities.Atom, known []string) labelledBatch {
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
func (uc RebranchClaims) write(ctx context.Context, a entities.Atom) error {
	if uc.AtomStore != nil {
		if _, err := uc.AtomStore.Write(ctx, a); err != nil {
			return err
		}
	}
	return uc.Atoms.SaveAtom(ctx, a)
}

// rebuildVocabulary writes the branch vocabulary from what the claims NOW say, and drops the alias
// table: the merges it records are the collapse this pass is repairing — a name that came back must not
// be mapped away again, and a true synonym is re-decided by the next curation (which no longer aims for
// a shorter list).
func (uc RebranchClaims) rebuildVocabulary(ctx context.Context) {
	if uc.Vocab == nil {
		return
	}
	branches, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return
	}
	names := make([]string, 0, len(branches))
	for _, b := range branches {
		names = append(names, b.Topic)
	}
	sort.Strings(names)
	existing, _ := uc.Vocab.Read(entities.VocabularyTopics)
	v := entities.Vocabulary{Namespace: entities.VocabularyTopics, Canonical: names, BroadTokens: existing.BroadTokens}
	if uc.Clock != nil {
		v.Updated = uc.Clock.Now()
	}
	if err := uc.Vocab.Write(v); err != nil && uc.Log != nil {
		uc.Log.Warn("rebranch: the branch vocabulary was not rebuilt", "err", err)
	}
}

func (uc RebranchClaims) phase(name string) {
	if uc.OnPhase != nil {
		uc.OnPhase(name)
	}
}

// counter is a small thread-safe counter the parallel questions share.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) add(d int) int { c.mu.Lock(); defer c.mu.Unlock(); c.n += d; return c.n }
