package usecases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/claims"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	coretree "github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// DistillSource is the DISTILL step: it exposes a source's outline as
// tools and lets the model write atoms with provenance to source nodes.
type DistillSource struct {
	// Parallel is how many parts of a document are derived at the same time. Parts are independent
	// (they only need the tree), so this divides the wall-clock of a long document.
	Parallel int
	// PartsPerRun bounds how many unclaimed parts one run distills, so a long document is derived
	// across several runs instead of losing everything to a timeout.
	PartsPerRun int
	// PartRepo records which parts are finished (a fact, not an inference from atoms).
	PartRepo interfaces.PartRepository
	// SourceLog records everything this run does to the source, where the source lives.
	SourceLog interfaces.SourceLog
	Index     interfaces.IndexRepository
	Trees     interfaces.TreeStore
	// Store reads the readable text a node's reference points into (the outline carries the reference,
	// not the text).
	Store    interfaces.SourceStore
	Atoms    interfaces.AtomStore
	AtomRepo interfaces.AtomRepository
	// Vocab is the DECIDED name vocabulary. Its alias table is applied at write time: a claim that
	// arrives with a name a curation absorbed is filed under the canonical one.
	Vocab interfaces.VocabularyStore
	// LinkResolver is optional: after a run, the atom→atom links created by the claims are resolved
	// by their target sentence. Without it the links stay raw (honest, but red).
	LinkResolver AtomLinkResolver
	Gateway      interfaces.AgentGateway
	Model        string
	// ReasoningEffort is passed to the model (see interfaces.AgentRequest).
	ReasoningEffort string
	MaxIterations   int
	Clock           interfaces.Clock
	Log             *slog.Logger
	// Steps records this step's state (distill: running/ok/failed) for the source's lights.
	Steps interfaces.StepTracker
}

// DistillResult summarizes the distill step.
type DistillResult struct {
	// Parts shows how much of the document this run covered; the next run continues the rest.
	PartsTotal int
	PartsDone  int
	PartsLeft  int
	// Atoms is the source's TOTAL after the run — not what this run wrote. The two are easy to confuse
	// (a second run reporting "187 atoms" after writing 65), so Written says what the run itself added.
	Atoms       int
	Written     int
	Iterations  int
	TotalTokens int
	Summary     string
}

// Distill runs the agent for one source and records steps on rec. It reports the DISTILL step's
// state: what is left open (parts without a claim) is what a later run continues.
func (uc DistillSource) Distill(ctx context.Context, hash string, rec *StepRecorder) (DistillResult, error) {
	recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepDistill, entities.StepRunning, "", nil)
	res, err := uc.distill(ctx, hash, rec)
	if err != nil {
		recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepDistill, entities.StepFailed, "", err)
		return res, err
	}
	detail := fmt.Sprintf("%d/%d parts · %d atoms", res.PartsDone, res.PartsTotal, res.Atoms)
	state := entities.StepOK
	if res.PartsLeft > 0 {
		// The run ENDED here. It is not a failure, and it is not running either: it stopped with work
		// left, and the next run continues exactly here.
		//
		// This used to be recorded as `running`, which made every unfinished source blink in the UI
		// forever — many blinking lights next to a title bar that correctly said "distill 2 sources".
		// `pending` is the honest state (nothing is happening), and the DETAIL carries the truth:
		// "12/25 parts · 3 open".
		state = entities.StepPending
		detail += fmt.Sprintf(" · %d open", res.PartsLeft)
	}
	recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepDistill, state, detail, nil)
	return res, nil
}

func (uc DistillSource) distill(ctx context.Context, hash string, rec *StepRecorder) (DistillResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return DistillResult{}, err
	}
	nodes, err := uc.Trees.Read(ctx, hash)
	if err != nil {
		return DistillResult{}, err
	}
	// The outline holds structure and references: the text of each part is resolved from the source's
	// readable text, once for the whole tree.
	nodes, err = nodeTexts(ctx, uc.Index, uc.Store, hash, nodes)
	if err != nil {
		return DistillResult{}, err
	}

	nodeByID := make(map[string]entities.SourceNode, len(nodes))
	for _, n := range nodes {
		nodeByID[n.NodeID] = n
	}

	// created counts the atoms the tools write. Work units run in parallel and share this counter,
	// so it is atomic — a plain int++ here is a data race on every multi-part source.
	var created atomic.Int64
	tools := []agents.Tool{
		{
			Def: interfaces.AgentToolDef{
				Name:        "list_topics",
				Description: "List the existing claim topics with their counts, so you reuse names.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				topics, err := uc.AtomRepo.ListClaimTopics(ctx)
				if err != nil {
					return "", err
				}
				m := make([]map[string]any, 0, len(topics))
				for _, b := range topics {
					m = append(m, map[string]any{"topic": b.Topic, "count": b.Count})
				}
				out, _ := json.Marshal(m)
				return string(out), nil
			},
		},
		{
			Def: interfaces.AgentToolDef{
				Name:        "read_source_node",
				Description: "Return the full text of a source node by its node_id.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"node_id":{"type":"string"}},"required":["node_id"]}`),
			},
			Run: func(_ context.Context, args json.RawMessage) (string, error) {
				var in struct {
					NodeID string `json:"node_id"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				n, ok := nodeByID[in.NodeID]
				if !ok {
					return "", fmt.Errorf("unknown node_id %q", in.NodeID)
				}
				text := n.Text
				if strings.TrimSpace(text) == "" {
					return "(empty part)", nil
				}
				const maxChars = 8000
				if len(text) > maxChars {
					text = text[:maxChars] + "\n… (truncated)"
				}
				return text, nil
			},
		},
	}

	// The name vocabulary is a DECISION: an absorbed topic name is mapped onto its canonical one here,
	// at the write, so the drift the curation repaired does not reappear on the next run.
	topicAliases := vocabularyAliases(uc.Vocab, entities.VocabularyTopics)

	// writeClaims is the APP's write, and it is the only one: the model answered with data, and this
	// records each claim. A failed write is reported, and the part stays open — never a silent drop.
	writeClaims := func(ctx context.Context, in []writeAtomArgs, defaultNode string) (int, int, error) {
		written, rejected, firstErr := 0, 0, error(nil)
		for _, c := range in {
			if strings.TrimSpace(c.Claim) == "" {
				continue
			}
			if _, err := uc.writeClaim(ctx, hash, nodeByID, c, topicAliases, defaultNode); err != nil {
				if errors.Is(err, claims.ErrNotStandalone) {
					// A claim about the source's structure is not knowledge. It is dropped, never
					// written: the part stays open (or is re-read), and lint can show the pattern.
					uc.log(ctx, rec.ID(), hash, "warn", "distill", "claim rejected: not standalone",
						map[string]any{"claim": oneLine(c.Claim, 160), "topic": c.Topic, "err": err.Error()})
					rejected++
					continue
				}
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			created.Add(1)
			written++
		}
		return written, rejected, firstErr
	}

	// A part is a subtree of the tree. Whether a part is FINISHED is a recorded
	// fact (distill_parts), never an inference from "it has atoms": a run can stop
	// mid-part, and such a part must be derived again instead of being skipped.
	states := map[string]entities.PartState{}
	if uc.PartRepo != nil {
		if m, err := uc.PartRepo.PartStates(ctx, src.ID); err == nil {
			states = m
		}
	}
	units := unitsOf(nodes)
	parts := units // one part per text-bearing node
	budget := uc.PartsPerRun
	if budget <= 0 {
		budget = 1
	}

	runID := rec.ID()

	// THE RECORDS MUST NOT CLAIM A DERIVATION THAT LEFT NOTHING. If every part is recorded finished
	// while the source has no claim, the records hang on something that is gone — the same rule the
	// pipeline itself follows. Re-arm them HERE, in the same run, instead of asking the reader to press
	// reset for every source (measured: 15 sources looping through "nothing was derived" on every
	// sweep, because the records were re-read and believed every time).
	if len(parts) > 0 && len(states) > 0 && uc.countAtoms(ctx, hash) == 0 {
		allDone, onlySourceRelative := true, true
		for _, part := range parts {
			st, ok := states[partHead(part)]
			if !ok || !st.Finished() {
				allDone = false
				break
			}
			if st.Reason != "source_relative" {
				onlySourceRelative = false
			}
		}
		if allDone && onlySourceRelative {
			allDone = false // a document made only of metadata correctly has no claim
		}
		if allDone && uc.PartRepo != nil {
			if err := uc.PartRepo.ClearParts(ctx, src.ID); err == nil {
				states = map[string]entities.PartState{}
				uc.log(ctx, runID, hash, "warn", "distill",
					"the part records claimed a finished derivation with no claim — cleared, deriving again",
					map[string]any{"parts": len(parts)})
			}
		}
	}

	// Which parts does this run take? The ones that are not recorded finished, up to the
	// budget. A part recorded done whose claims are GONE is not finished in fact: its record
	// describes a derivation that no longer exists, so it is derived again (the same rule that
	// re-arms a changed part). Without this, deleting one claim of a finished part left the source
	// with the rest of its claims and a part that could never be repaired.
	claimedNodes, refsOK := uc.claimedNodeSet(ctx, hash)
	var todo [][]entities.SourceNode
	for _, part := range parts {
		if len(todo) >= budget {
			break
		}
		head := partHead(part)
		if st, ok := states[head]; ok && st.Finished() {
			if refsOK && !claimedNodes[head] && st.Reason != "source_relative" {
				uc.log(ctx, runID, hash, "warn", "distill", "part recorded done but has no claim — redoing", map[string]any{
					"part": head, "state": string(st.State), "atoms": st.Atoms,
				})
			} else {
				uc.log(ctx, runID, hash, "info", "distill", "part already finished", map[string]any{
					"part": head, "state": string(st.State), "atoms": st.Atoms, "skipped": true,
					"finished_at": st.UpdatedAt.Format("2006-01-02T15:04:05Z"),
				})
				continue
			}
		}
		todo = append(todo, part)
	}

	// Parts are independent of each other (they only need the tree), so they are derived
	// with bounded concurrency: a long document's wall-clock divides by the width.
	workers := min(max(uc.Parallel, 1), len(todo))
	var (
		mu         sync.Mutex
		wg         sync.WaitGroup
		steps      int
		failed     int
		firstErr   string
		iterations int
		tokens     int
		hardErr    error
	)
	sem := make(chan struct{}, workers)
	for _, part := range todo {
		wg.Add(1)
		go func(part []entities.SourceNode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			err := uc.runPart(ctx, runID, hash, src, part, tools, writeClaims, states,
				&mu, &steps, &failed, &firstErr, &iterations, &tokens, rec)
			if err == nil || isMaxIterations(err) {
				return
			}
			mu.Lock()
			if hardErr == nil {
				hardErr = err
			}
			mu.Unlock()
		}(part)
	}
	wg.Wait()

	totalAtoms := uc.countAtoms(ctx, hash)
	partsLeft := 0
	for _, part := range parts {
		if st, ok := states[partHead(part)]; !ok || !st.Finished() {
			partsLeft++
		}
	}
	if hardErr != nil {
		return DistillResult{Atoms: totalAtoms, Iterations: iterations, TotalTokens: tokens}, hardErr
	}

	// A run that derived NOTHING is an ERROR, whatever the reason. "distill produced no atoms" read like
	// a state the derivation could legitimately be in — as if the model had read the document and found
	// nothing — while the truth was that it never wrote a claim: no tool call, or every call failed. A
	// document with parts always has claims, so nothing-here is a failure the reader has to see (the
	// parts stay open, the light stays amber, and this error says why).
	if totalAtoms == 0 && len(todo) == 0 && len(parts) > 0 {
		if allPartsSourceRelative(parts, states) {
			// The part records are the step's evidence. They go into the .meta on THIS path too:
			// returning before the mirror left a record that said "ok" while knowing no part, and
			// the pipeline's completeness check then re-derived the same source for ever.
			uc.mirrorParts(ctx, hash, src.ID)
			return DistillResult{
				Atoms: 0, PartsDone: len(parts), PartsTotal: len(parts), PartsLeft: 0,
				Iterations: iterations, TotalTokens: tokens, Summary: "no claim — the parts are link lists or metadata",
			}, nil
		}
		// Nothing ran, because nothing was OWED: every part is already recorded as finished. That is not
		// "the model wrote no claim" — the model was never asked. The records claim a derivation that
		// left no claim behind, so they hang on something that is gone; the same rule as the pipeline
		// itself says they cannot stand. Re-arm them HERE instead of asking the reader to press reset
		// for every source (measured: 15 sources looping through this error on every sweep).
		cleared := false
		if uc.PartRepo != nil {
			if err := uc.PartRepo.ClearParts(ctx, src.ID); err == nil {
				cleared = true
			}
		}
		if cleared {
			return DistillResult{Atoms: 0, PartsLeft: len(parts), Iterations: 0, TotalTokens: 0},
				fmt.Errorf("the part records claimed every part (%d) was finished, but the source has no claim — the records hang on a derivation that left nothing, so they were cleared and the source is derived again", len(parts))
		}
		return DistillResult{Atoms: 0, PartsLeft: len(parts), Iterations: 0, TotalTokens: 0},
			fmt.Errorf("nothing was derived: all %d part(s) are recorded as finished, but the source has no claim — the part records and the vault disagree; reset the source to derive it again", len(parts))
	}
	// The part records go into the source's .meta file as well as into the index: the claims, the
	// outline and the rendering are files a reindex rebuilds from, and the part record has to be one
	// too — otherwise a recreated index shows green lights over "0 of 161 parts done".
	uc.mirrorParts(ctx, hash, src.ID)
	if totalAtoms == 0 && allPartsSourceRelative(parts, states) {
		// Every text-bearing part was document metadata (chapters, reading order, the book): there is
		// no claim to write, and that is a COMPLETE derivation, not a failed one. It must not block
		// every later source at the global distill barrier.
		return DistillResult{
			Atoms: 0, PartsDone: len(parts), PartsTotal: len(parts), PartsLeft: 0,
			Iterations: iterations, TotalTokens: tokens, Summary: "no claim — the parts are link lists or metadata",
		}, nil
	}
	if totalAtoms == 0 {
		if firstErr != "" {
			return DistillResult{Atoms: 0, PartsLeft: partsLeft, Iterations: iterations, TotalTokens: tokens},
				fmt.Errorf("no claims were extracted: %d tool call(s) failed, first: %s", failed, firstErr)
		}
		if uc.Log != nil {
			uc.Log.Error("distill wrote no claim at all", "hash", hash, "steps", steps, "parts", len(parts))
		}
		return DistillResult{Atoms: 0, PartsLeft: partsLeft, Iterations: iterations, TotalTokens: tokens},
			fmt.Errorf("the model wrote no claim at all (%d parts, %d tool call(s)) — a document with parts always has claims, so this is a failed derivation, not an empty one", len(parts), steps)
	}
	if uc.LinkResolver != nil {
		if n, err := uc.LinkResolver.ResolveLinks(ctx); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("distill: atom links not resolved", "hash", hash, "err", err)
			}
		} else if n > 0 {
			uc.log(ctx, runID, hash, "info", "distill", "atom links resolved", map[string]any{"links": n})
		}
	}
	return DistillResult{
		Atoms: totalAtoms, Written: int(created.Load()),
		PartsTotal: len(parts), PartsDone: len(parts) - partsLeft,
		PartsLeft: partsLeft, Iterations: iterations, TotalTokens: tokens,
	}, nil
}

// writeClaimsFunc is the app's write: the claims the model ANSWERED with, recorded. It is a function
// because the parts run in parallel and the write counts into the shared result.
type writeClaimsFunc func(ctx context.Context, claims []writeAtomArgs, defaultNode string) (int, int, error)

// distillAnswer is the structured answer of one part: the claims it carries. `complete` is the audit's
// word — "nothing is missing" — and it is an answer, not a tool call.
type distillAnswer struct {
	Complete bool            `json:"complete"`
	Claims   []writeAtomArgs `json:"claims"`
}

// parseDistillAnswer reads the JSON object out of the model's answer, tolerating a code fence and a
// sentence around it. A bare COMPLETE is accepted because an older prompt used it.
func parseDistillAnswer(content string) (distillAnswer, error) {
	trimmed := strings.TrimSpace(content)
	if strings.EqualFold(strings.Trim(trimmed, ".: "), "COMPLETE") {
		return distillAnswer{Complete: true}, nil
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return distillAnswer{}, fmt.Errorf("the answer carries no JSON object — tools only read, so the claims are the ANSWER ({\"claims\":[…]})")
	}
	var a distillAnswer
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &a); err != nil {
		return distillAnswer{}, fmt.Errorf("the answer is not valid JSON: %w", err)
	}
	return a, nil
}

// salvageClaims pulls the COMPLETE claim objects out of an answer that was cut off by the output
// limit. The array is broken; the objects inside it need not be.
func salvageClaims(content string) []writeAtomArgs {
	var out []writeAtomArgs
	var stack []int
	inStr, esc := false, false
	for i, r := range content {
		if inStr {
			switch {
			case esc:
				esc = false
			case r == '\\':
				esc = true
			case r == '"':
				inStr = false
			}
			continue
		}
		switch r {
		case '"':
			inStr = true
		case '{':
			stack = append(stack, i)
		case '}':
			if len(stack) == 0 {
				continue
			}
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			var c writeAtomArgs
			if json.Unmarshal([]byte(content[start:i+1]), &c) == nil && strings.TrimSpace(c.Claim) != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// writeClaim records ONE claim the model answered with. It is the distill's only writer of an atom —
// there is no tool that can write.
func (uc DistillSource) writeClaim(ctx context.Context, hash string, nodeByID map[string]entities.SourceNode, in writeAtomArgs, topicAliases map[string]string, defaultNode string) (entities.Atom, error) {
	now := uc.Clock.Now()
	a := entities.Atom{
		ID:        ulid.Make(),
		Claim:     strings.TrimSpace(in.Claim),
		Body:      strings.TrimSpace(in.Body),
		Topic:     resolveVocabulary(topicAliases, coremarkdown.NormalizeTopic(in.Topic)),
		Type:      atomType(in.Type),
		Status:    entities.AtomActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if issues := claims.Issues(a.Topic, a.Claim, a.Body); len(issues) > 0 {
		return entities.Atom{}, claims.NotStandaloneError{Reasons: issues}
	}
	var unknown []string
	for _, nodeID := range in.Nodes {
		n, ok := nodeByID[nodeID]
		if !ok {
			unknown = append(unknown, nodeID)
			continue
		}
		a.Sources = append(a.Sources, entities.AtomSource{Hash: hash, NodeID: nodeID, OCR: n.OCR})
	}
	if len(unknown) > 0 {
		// A provenance row is the claim's trace back to the text. Dropping one silently would make
		// the atom un-attributable for that part, so the write is rejected instead.
		return entities.Atom{}, fmt.Errorf("unknown source nodes: %v", unknown)
	}
	if len(a.Sources) == 0 {
		// No node named: the claim belongs to the PART it was derived from. A source-wide claim
		// (empty node) cannot cover a part in the completeness check, so it would make the part look
		// claim-less and re-derive it forever.
		if defaultNode != "" {
			a.Sources = append(a.Sources, entities.AtomSource{Hash: hash, NodeID: defaultNode, OCR: nodeByID[defaultNode].OCR})
		} else {
			a.Sources = append(a.Sources, entities.AtomSource{Hash: hash})
		}
	}
	for _, l := range coremarkdown.Wikilinks(a.Body) {
		a.Links = append(a.Links, entities.Link{RawTarget: l.RawTarget, Alias: l.Alias, Heading: l.Heading, Position: l.Position})
	}
	stored, err := uc.Atoms.Write(ctx, a)
	if err != nil {
		return entities.Atom{}, err
	}
	if err := uc.AtomRepo.SaveAtom(ctx, stored); err != nil {
		// The file is written FIRST (it is the source of truth) and the index row second. A write that
		// cannot be recorded must not leave the file behind: an atom the index never saw is an orphan —
		// invisible to every page, every count and every cleanup.
		if rmErr := uc.Atoms.Remove(ctx, stored); rmErr != nil && uc.Log != nil {
			uc.Log.Warn("could not remove the atom file of a failed write",
				"atom", stored.ID.String(), "path", stored.Path, "err", rmErr)
		}
		return entities.Atom{}, err
	}
	return stored, nil
}

type writeAtomArgs struct {
	Topic string   `json:"topic"`
	Claim string   `json:"claim"`
	Body  string   `json:"body"`
	Type  string   `json:"type"`
	Nodes []string `json:"nodes"`
}

func atomType(s string) entities.AtomType {
	t := entities.AtomType(strings.ToLower(strings.TrimSpace(s)))
	switch t {
	case entities.AtomFact, entities.AtomDefinition, entities.AtomClaim, entities.AtomExample, entities.AtomCounterpoint, entities.AtomQuote:
		return t
	default:
		return entities.AtomClaim
	}
}

// partsLeftOf counts the parts that are not finished yet.
func partsLeftOf(parts [][]entities.SourceNode, states map[string]entities.PartState) int {
	left := 0
	for _, part := range parts {
		if st, ok := states[partHead(part)]; !ok || !st.Finished() {
			left++
		}
	}
	return left
}

// partHead is the top-level node id of a part (its identity for the record).
func partHead(part []entities.SourceNode) string {
	if len(part) == 0 {
		return ""
	}
	return part[0].NodeID
}

// unitBudget is the turn budget of ONE part: writing a part's claims is a couple of turns, its
// completeness check a couple more. A part cannot need a document-sized budget — the parts are
// small by construction, which is the point.
func unitBudget(base int) int {
	if base < 6 {
		return 6
	}
	if base > 14 {
		return 14
	}
	return base
}

// mirrorParts writes a source's part records into its .meta file. The part records are the step's
// evidence, and the .meta is the record a reindex rebuilds from: a run that finished its parts but
// left the record without them makes the pipeline read a record that no longer knows its derivation.
// Every exit path that leaves a finished derivation calls this, including the early one for a
// document made only of metadata.
func (uc DistillSource) mirrorParts(ctx context.Context, hash string, sourceID ulid.ULID) {
	if uc.Steps == nil || uc.PartRepo == nil {
		return
	}
	states, err := uc.PartRepo.PartStates(ctx, sourceID)
	if err != nil || len(states) == 0 {
		return
	}
	records := make(map[string]entities.PartRecord, len(states))
	for node, st := range states {
		records[node] = entities.PartRecord{
			State: st.State, Reason: st.Reason, Atoms: st.Atoms, At: st.UpdatedAt,
		}
	}
	if err := uc.Steps.SetParts(hash, records); err != nil && uc.Log != nil {
		uc.Log.Warn("distill: could not record the parts", "hash", hash, "err", err)
	}
}

// allPartsSourceRelative reports a document that carries no knowledge of its own: every part it
// derived was source metadata and correctly produced no atom.
func allPartsSourceRelative(parts [][]entities.SourceNode, states map[string]entities.PartState) bool {
	if len(parts) == 0 || len(states) == 0 {
		return false
	}
	for _, part := range parts {
		st, ok := states[partHead(part)]
		if !ok || st.State != entities.PartDone || st.Reason != "source_relative" {
			return false
		}
	}
	return true
}

// incompleteReason names why a part is not finished.
func incompleteReason(err error) string {
	if isMaxIterations(err) {
		return "max_iterations"
	}
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "aborted"
	}
	return "error"
}

// isMaxIterations reports whether the loop stopped because it ran out of turns.
func isMaxIterations(err error) bool {
	return err != nil && strings.Contains(err.Error(), "max iterations")
}

// partLevel maps a part outcome to a log level.
func partLevel(st entities.PartState) string {
	if st.Finished() {
		return "info"
	}
	return "warn"
}

// dropPartAtoms removes the source's atoms that cite ONLY nodes of this part, so an
// unfinished part can be derived again without duplicating what it already produced.
// Atoms that span several parts are left alone: another part's work depends on them.
func (uc DistillSource) dropPartAtoms(ctx context.Context, hash string, part []entities.SourceNode) int {
	refs, err := uc.AtomRepo.NodeRefsBySource(ctx, hash)
	if err != nil {
		return 0
	}
	atoms, err := uc.AtomRepo.ListAtomsBySource(ctx, hash)
	if err != nil {
		return 0
	}
	inside := make(map[string]bool, len(part))
	for _, n := range part {
		inside[n.NodeID] = true
	}
	removed := 0
	for _, a := range atoms {
		nodes := refs[a.ID]
		if len(nodes) == 0 {
			continue
		}
		only := true
		for _, n := range nodes {
			if !inside[n] {
				only = false
				break
			}
		}
		if !only {
			continue
		}
		if err := uc.AtomRepo.DeleteAtom(ctx, a.ID); err != nil {
			continue
		}
		_ = uc.Atoms.Remove(ctx, a)
		removed++
	}
	return removed
}

// log records one source-scoped event (no-op when no log is configured).
func (uc DistillSource) log(ctx context.Context, runID ulid.ULID, hash, level, phase, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	id := ""
	if runID != (ulid.ULID{}) {
		id = runID.String()
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), RunID: id, Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

// unitsOf returns a source's work units: ONE unit per node that carries text, in document order.
//
// The unit is deliberately the smallest thing that carries text. That is what makes completeness
// simple: a unit is done when ITS OWN text has been turned into claims — a fact about one node,
// which the run verifies by reading the text against the claims. Splitting a document into a few
// large parts made completeness an inference ("was this node cited somewhere?") and let a whole
// part be recorded as finished with most of its statements still unclaimed.
func unitsOf(nodes []entities.SourceNode) [][]entities.SourceNode {
	units := make([][]entities.SourceNode, 0, len(nodes))
	for _, n := range nodes {
		if strings.TrimSpace(n.Text) == "" {
			continue // a structural node carries no claim of its own
		}
		if strings.TrimSpace(n.Title) == "" && coretree.IsNavigationText(n.Text) {
			continue // a breadcrumb/menu is page chrome, not a part
		}
		units = append(units, []entities.SourceNode{n})
	}
	return units
}

// unitLabel names the work item: the node itself, with how much text it carries.
func unitLabel(part []entities.SourceNode) string {
	if len(part) == 0 {
		return ""
	}
	n := part[0]
	return fmt.Sprintf("%s — %s (%d chars)", n.NodeID, oneLine(n.Title, 60), len(strings.TrimSpace(n.Text)))
}

func outlineOf(nodes []entities.SourceNode) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.NodeID == "0" {
			continue
		}
		title := n.Title
		if title == "" {
			title = "(untitled)"
		}
		page := ""
		if n.StartPage > 0 {
			page = fmt.Sprintf(" — p.%d", n.StartPage)
			if n.EndPage > n.StartPage {
				page += fmt.Sprintf("–%d", n.EndPage)
			}
		}
		indent := strings.Repeat("  ", n.Depth)
		fmt.Fprintf(&b, "%s%s — %s%s\n", indent, n.NodeID, title, page)
		if snippet := oneLine(n.Text, 160); snippet != "" {
			fmt.Fprintf(&b, "%s    %s\n", indent, snippet)
		}
	}
	return b.String()
}

// oneLine collapses whitespace and truncates, for a compact outline snippet.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}

// citedNodes returns the node ids of this part that an atom of the source cites.
func (uc DistillSource) citedNodes(ctx context.Context, hash string, part []entities.SourceNode) map[string]bool {
	out := map[string]bool{}
	refs, err := uc.AtomRepo.NodeRefsBySource(ctx, hash)
	if err != nil {
		return out
	}
	inside := make(map[string]bool, len(part))
	for _, n := range part {
		inside[n.NodeID] = true
	}
	for _, nodes := range refs {
		for _, node := range nodes {
			if inside[node] {
				out[node] = true
			}
		}
	}
	return out
}

// atomsInPart counts the source's atoms that cite at least one node of this part.
func (uc DistillSource) atomsInPart(ctx context.Context, hash string, part []entities.SourceNode) int {
	refs, err := uc.AtomRepo.NodeRefsBySource(ctx, hash)
	if err != nil {
		return 0
	}
	inside := make(map[string]bool, len(part))
	for _, n := range part {
		inside[n.NodeID] = true
	}
	count := 0
	for _, nodes := range refs {
		for _, node := range nodes {
			if inside[node] {
				count++
				break
			}
		}
	}
	return count
}

// countAtoms returns how many atoms of this source exist right now.
func (uc DistillSource) countAtoms(ctx context.Context, hash string) int {
	atoms, err := uc.AtomRepo.ListAtomsBySource(ctx, hash)
	if err != nil {
		return 0
	}
	return len(atoms)
}

// finishedParts counts the source's parts recorded as done.
func (uc DistillSource) finishedParts(ctx context.Context, sourceID ulid.ULID) int {
	if uc.PartRepo == nil {
		return 0
	}
	states, err := uc.PartRepo.PartStates(ctx, sourceID)
	if err != nil {
		return 0
	}
	n := 0
	for _, st := range states {
		if st.Finished() {
			n++
		}
	}
	return n
}

// claimedNodeSet returns the node ids of a source that at least one atom cites, and whether the
// read succeeded. A failed read must not make every finished part look lost: that would re-derive
// the whole document on a transient database error.
func (uc DistillSource) claimedNodeSet(ctx context.Context, hash string) (map[string]bool, bool) {
	refs, err := uc.AtomRepo.NodeRefsBySource(ctx, hash)
	if err != nil {
		return nil, false
	}
	out := make(map[string]bool, len(refs))
	for _, nodes := range refs {
		for _, n := range nodes {
			out[n] = true
		}
	}
	return out, true
}

// partText renders the part's text. A part is small, so it is handed over whole; the cap only
// exists for a node that turned out to be a whole chapter, and then the model reads the rest itself.
func partText(part []entities.SourceNode) string {
	const perNode = 8000
	var b strings.Builder
	for _, n := range part {
		text := strings.TrimSpace(n.Text)
		if text == "" {
			continue
		}
		if len(text) > perNode {
			text = text[:perNode] + "… (cut — read_source_node for the rest)"
		}
		fmt.Fprintf(&b, "%s\n", text)
	}
	return strings.TrimSpace(b.String())
}

// runPart derives ONE part (one text-bearing node): it drops what an earlier attempt left
// inside it, runs the agent and records the outcome. Parts do not depend on each other, so several
// of these run at the same time (bounded by pipeline.parallel_parts).
func (uc DistillSource) runPart(
	ctx context.Context, runID ulid.ULID, hash string, src entities.Source, part []entities.SourceNode,
	tools []agents.Tool, write writeClaimsFunc, states map[string]entities.PartState,
	mu *sync.Mutex, steps, failed *int, firstErr *string, iterations, tokens *int, rec *StepRecorder,
) error {
	head := partHead(part)
	rejected := 0

	// Whatever an earlier attempt left inside this part goes first: a half-done part is
	// re-derived as a whole, never half-duplicated.
	if n := uc.dropPartAtoms(ctx, hash, part); n > 0 {
		mu.Lock()
		reason := "no record"
		if st, ok := states[head]; ok {
			reason = st.Reason
		}
		mu.Unlock()
		uc.log(ctx, runID, hash, "warn", "distill", "redoing an unfinished part", map[string]any{
			"part": head, "reason": reason, "removed_atoms": n,
		})
	}
	uc.log(ctx, runID, hash, "info", "distill", "part start", map[string]any{
		"part": head, "label": unitLabel(part), "chars": len(strings.TrimSpace(part[0].Text)),
	})

	partFields := map[string]any{"part": head}
	onStep := func(si agents.StepInfo) {
		mu.Lock()
		(*steps)++
		if si.Err != nil {
			(*failed)++
			if *firstErr == "" {
				*firstErr = fmt.Sprintf("%s: %v", si.Tool, si.Err)
			}
		}
		rec.OnStep(si) // the recorder writes rows: serialise it
		mu.Unlock()
		logToolCall(ctx, uc.SourceLog, uc.Clock, hash, "distill", runID.String(), partFields, si)
	}
	onTurn := func(iteration int, resp interfaces.AgentResponse, err error) {
		logTurn(ctx, uc.SourceLog, uc.Clock, hash, "distill", runID.String(), partFields, iteration, resp, err)
	}
	loop := agents.Loop{
		Gateway:         uc.Gateway,
		Model:           uc.Model,
		System:          agents.DistillSystem,
		Tools:           tools,
		ReasoningEffort: uc.ReasoningEffort,
		MaxIterations:   unitBudget(uc.MaxIterations),
		OnStep:          onStep,
		OnTurn:          chainTurns(turnLogger(uc.Log, "distill"), onTurn),
		OnRetry:         logRetry(ctx, uc.SourceLog, uc.Clock, hash, "distill", runID.String(), partFields),
	}
	// ONE part is the work unit, and it is COMPLETE when its own text has been turned into claims.
	// That is checked, not inferred: once the claims are written, the model reads the part's text
	// against them and writes whatever no claim covers. The old flow inferred completeness from a
	// citation count over a whole part — which is how 25 nodes ended up "covered" by 36 compound
	// claims while statements inside them had no atom at all.
	var (
		res           agents.Result
		err           error
		elided        int
		truncated     bool
		auditComplete bool
		passes        int
	)
	unitID := head
	for pass := 1; pass <= maxUnitPasses; pass++ {
		claims := uc.claimsOfUnit(ctx, hash, unitID)
		// The write pass comes first, then the audit — that is the pass which CLOSES the part.
		// A cut-off answer takes the next pass (it was not the model's last word), and only once
		// nothing is missing any more does the audit run and finish the part.
		audit := pass > 1 && !truncated
		var prompt string
		switch {
		case pass == 1:
			prompt = agents.DistillPartUser(src.Title, hash, unitID, part[0].Title, partText(part))
		case truncated:
			prompt = agents.DistillPartTruncated(unitID, part[0].Title, bulletClaims(claims))
		default:
			prompt = agents.DistillPartAudit(unitID, part[0].Title, partText(part), bulletClaims(claims))
		}
		var passRes agents.Result
		passRes, err = loop.Run(ctx, prompt)
		passes++
		res.Iterations += passRes.Iterations
		res.Usage.PromptTokens += passRes.Usage.PromptTokens
		res.Usage.CompletionTokens += passRes.Usage.CompletionTokens
		res.Usage.TotalTokens += passRes.Usage.TotalTokens
		elided += passRes.Elided
		res.Content = passRes.Content
		truncated = passRes.Truncated
		if err != nil {
			break
		}
		if truncated {
			// A cut-off answer is not valid JSON — but the claim objects it DID finish are complete, and
			// throwing them away would re-derive work the model already did. Salvage them, then continue.
			if salvaged := salvageClaims(passRes.Content); len(salvaged) > 0 {
				_, rej, wErr := write(ctx, salvaged, unitID)
				rejected += rej
				if wErr != nil {
					err = wErr
					break
				}
			}
			continue
		}
		// THE MODEL ANSWERED; THE APP WRITES. The answer is data — a tool call is a round-trip per
		// claim, and "the model answered in prose" stopped being a failure the moment the write became
		// an answer instead of a tool.
		answer, pErr := parseDistillAnswer(passRes.Content)
		if pErr != nil {
			err = pErr
			break
		}
		if len(answer.Claims) > 0 {
			_, rej, wErr := write(ctx, answer.Claims, unitID)
			rejected += rej
			if wErr != nil {
				err = wErr
				break
			}
		}
		if audit {
			auditComplete = answer.Complete
			break // the part was read against its claims: nothing is missing any more
		}
	}
	mu.Lock()
	*iterations += res.Iterations
	*tokens += res.Usage.TotalTokens
	if elided > 0 {
		res.Elided = elided
	}
	mu.Unlock()

	atoms := uc.atomsInPart(ctx, hash, part)
	cited := uc.citedNodes(ctx, hash, part)
	state := entities.PartState{
		NodeID: unitID, State: entities.PartDone, Atoms: atoms,
		RunID: runID, UpdatedAt: uc.Clock.Now(),
	}
	switch {
	case truncated:
		// The answer was cut off: that is the reason, whatever the loop reported as well — the part
		// was not proven complete, and the next run continues it.
		state.State = entities.PartIncomplete
		state.Reason = "truncated"
	case err != nil:
		state.State = entities.PartIncomplete
		state.Reason = incompleteReason(err)
		// Cut off again, so this part is not proven complete: it is derived again next time.
		state.State = entities.PartIncomplete
		state.Reason = "truncated"
	case atoms == 0 && (rejected > 0 || auditComplete):
		// The model read the part, offered nothing that could stand alone (or offered only
		// document-relative statements that were rejected), and the audit found nothing missing.
		// This is document metadata — an appendix intro, a surviving cover/front-matter part — not
		// knowledge: there is nothing to write, and retrying the same text would produce the same
		// non-claims. It is done with zero atoms and reported as source_relative.
		state.State = entities.PartDone
		state.Reason = "source_relative"
	case atoms == 0:
		// A part that carries text MUST produce a claim: 0 claims means the derivation did not do its
		// job, not that the part is legitimately empty. So it is NOT recorded as done — it stays open,
		// which is what makes the next run derive it again (and what keeps the light amber instead of
		// claiming a green with nothing behind it).
		state.State = entities.PartIncomplete
		state.Reason = "no_claims"
	}
	if uc.PartRepo != nil {
		if sErr := uc.PartRepo.SetPartState(ctx, src.ID, state); sErr != nil && uc.Log != nil {
			uc.Log.Warn("distill: record part state", "part", unitID, "err", sErr)
		}
	}
	mu.Lock()
	states[unitID] = state
	mu.Unlock()
	uc.log(ctx, runID, hash, partLevel(state), "distill", "part finished", map[string]any{
		"part": unitID, "state": string(state.State), "reason": state.Reason,
		// atoms = this part's claims; source_atoms = the whole source's, which is what a live view
		// shows. Without it the UI showed the last part's count as if it were the total.
		"atoms": atoms, "source_atoms": uc.countAtoms(ctx, hash),
		"passes": passes, "iterations": res.Iterations, "tokens": res.Usage.TotalTokens,
		"elided_results": res.Elided, "parts_done": uc.finishedParts(ctx, src.ID),
		"cited_nodes": len(cited), "nodes_with_text": 1, "chars": len(strings.TrimSpace(part[0].Text)),
	})
	if err != nil && isMaxIterations(err) {
		// Incomplete, but the run makes progress on the other parts; it will be redone.
		uc.log(ctx, runID, hash, "warn", "distill", "part hit the iteration cap, will be redone", map[string]any{
			"part": unitID, "err": err,
		})
		return nil
	}
	return err
}

// maxUnitPasses bounds the work on ONE part: write its claims, then audit it — twice, when an
// answer was cut off in between.
const maxUnitPasses = 4

// claimsOfUnit lists the claims that exist for one part; the audit compares them with its text.
func (uc DistillSource) claimsOfUnit(ctx context.Context, hash, nodeID string) []string {
	refs, err := uc.AtomRepo.NodeRefsBySource(ctx, hash)
	if err != nil {
		return nil
	}
	var out []string
	for atomID, nodes := range refs {
		cites := slices.Contains(nodes, nodeID)
		if !cites {
			continue
		}
		if a, err := uc.AtomRepo.GetAtom(ctx, atomID); err == nil && strings.TrimSpace(a.Claim) != "" {
			out = append(out, oneLine(a.Claim, 200))
		}
	}
	return out
}

// bulletClaims renders a part's claims for an audit prompt.
func bulletClaims(claims []string) string {
	if len(claims) == 0 {
		return "  (none yet)"
	}
	var b strings.Builder
	for _, c := range claims {
		fmt.Fprintf(&b, "  - %s\n", c)
	}
	return strings.TrimRight(b.String(), "\n")
}

// unitCovered reports whether the part's own text has a claim: with one node per unit, coverage is
// exactly that question — no percentage, no threshold.
func unitCovered(nodeID string, cited map[string]bool) bool { return cited[nodeID] }
