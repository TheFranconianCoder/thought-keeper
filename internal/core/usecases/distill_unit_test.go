package usecases_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// truncatedResponse is a turn the endpoint cut off at its output limit. The model never saw the
// cut, so it goes on as if it had finished its answer.
func truncatedResponse(name, args string) interfaces.AgentResponse {
	r := toolResponse(name, args)
	r.FinishReason = interfaces.FinishReasonLength
	return r
}

// distillFixture runs one distill over the given tree and hands back the gateway, so a test can see
// what the agent was actually asked.
func distillFixture(t *testing.T, nodes []entities.SourceNode, responses []interfaces.AgentResponse) (*fakeGateway, *fakeAtomRepo, *fakeSections, *fakeSourceLog) {
	t.Helper()
	gw := &fakeGateway{responses: responses}
	repo, parts, logs := runDistill(t, nodes, gw)
	return gw, repo, parts, logs
}

// runDistillErr is runDistill for a run that is EXPECTED to fail: the error is the point there, not a
// broken test.
func runDistillErr(t *testing.T, nodes []entities.SourceNode, gw interfaces.AgentGateway) (*fakeAtomRepo, *fakeSections, *fakeSourceLog, error) {
	t.Helper()
	repo, parts, logs, err := runDistillRaw(t, nodes, gw)
	return repo, parts, logs, err
}

func runDistill(t *testing.T, nodes []entities.SourceNode, gw interfaces.AgentGateway) (*fakeAtomRepo, *fakeSections, *fakeSourceLog) {
	t.Helper()
	ctx := context.Background()
	hash := strings.Repeat("ab", 32)

	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{hash: nodes}}
	atomRepo := &fakeAtomRepo{}
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Title: "A document",
		Kind: entities.SourceKindURL, Ext: ".md", TreeState: entities.TreeBuilt,
	}
	parts := &fakeSections{}
	logs := &fakeSourceLog{}
	az := usecases.DistillSource{
		Index: idx, Trees: trees, Atoms: atomStore, AtomRepo: atomRepo,
		PartRepo: parts, SourceLog: logs, PartsPerRun: 8, Parallel: 2,
		Gateway: gw, Model: "test", MaxIterations: 6, Clock: fixedClock{},
	}
	if _, err := az.Distill(ctx, hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil)); err != nil {
		t.Fatalf("distill: %v", err)
	}
	return atomRepo, parts, logs
}

// runDistillRaw is the same run without the "must succeed" assertion.
func runDistillRaw(t *testing.T, nodes []entities.SourceNode, gw interfaces.AgentGateway) (*fakeAtomRepo, *fakeSections, *fakeSourceLog, error) {
	t.Helper()
	ctx := context.Background()
	hash := strings.Repeat("ab", 32)
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{hash: nodes}}
	atomRepo := &fakeAtomRepo{}
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Title: "A document",
		Kind: entities.SourceKindURL, Ext: ".md", TreeState: entities.TreeBuilt,
	}
	parts := &fakeSections{}
	logs := &fakeSourceLog{}
	az := usecases.DistillSource{
		Index: idx, Trees: trees, Atoms: atomStore, AtomRepo: atomRepo,
		PartRepo: parts, SourceLog: logs, PartsPerRun: 8, Parallel: 2,
		Gateway: gw, Model: "test", MaxIterations: 6, Clock: fixedClock{},
	}
	_, err := az.Distill(ctx, hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil))
	return atomRepo, parts, logs, err
}

func promptsOf(gw *fakeGateway) []string {
	var out []string
	for _, req := range gw.requests {
		out = append(out, req.Messages[len(req.Messages)-1].Content)
	}
	return out
}

func anyPromptContains(gw *fakeGateway, want string) bool {
	for _, p := range promptsOf(gw) {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

func textNode(id, title, text string, depth int) entities.SourceNode {
	return entities.SourceNode{NodeID: id, Depth: depth, Title: title, Text: text}
}

// nodeIDAwareGateway answers from the PROMPT, not from a script: parts run in parallel, so the
// order of the requests is not defined and a scripted sequence would be a coin flip.
type nodeIDAwareGateway struct {
	mu    sync.Mutex
	asked []string // the node id of every prompt, in arrival order
}

func (g *nodeIDAwareGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	last := req.Messages[len(req.Messages)-1].Content
	tools := 0
	for _, m := range req.Messages {
		if m.Role == "tool" {
			tools++
		}
	}
	id := nodeIDInPrompt(last)
	g.mu.Lock()
	g.asked = append(g.asked, id)
	g.mu.Unlock()
	if id == "" || strings.Contains(last, "Check ONE part") {
		// The audit: nothing is missing.
		return completeResponse(), nil
	}
	args, _ := json.Marshal(map[string]any{
		"branch": "cpp", "claim": "Claim for node " + id + ".", "body": "b", "nodes": []string{id},
	})
	return claimsResponse(string(args)), nil
}

// nodeIDInPrompt reads the part's id out of "node <id> \"title\"".
func nodeIDInPrompt(prompt string) string {
	const marker = "node "
	_, after, ok := strings.Cut(prompt, marker)
	if !ok {
		return ""
	}
	rest := after
	if j := strings.IndexByte(rest, ' '); j > 0 {
		return rest[:j]
	}
	return ""
}

// TestDistillWorksOneTextNodeAtATime is the point of the whole flow: the work unit is ONE node with
// text, not a part subtree. A part is small, so its claims can be complete — the part-sized
// unit is what let a whole document be "covered" by a handful of compound claims.
func TestDistillWorksOneTextNodeAtATime(t *testing.T) {
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		textNode("1", "Games", "C++ is used for games.", 1),
		textNode("2", "Browsers", "Browsers render with C++.", 1),
	}
	gw := &nodeIDAwareGateway{}
	atomRepo, parts, logs := runDistill(t, nodes, gw)

	// Every node with text became its own work item — twice each (write pass + audit).
	seen := map[string]int{}
	for _, id := range gw.asked {
		seen[id]++
	}
	for _, id := range []string{"1", "2"} {
		if seen[id] == 0 {
			t.Errorf("node %s was never worked on its own; asked: %v", id, gw.asked)
		}
		if st, ok := parts.m[id]; !ok || st.State != entities.PartDone {
			t.Errorf("part %s: state = %+v, want a recorded done", id, st)
		}
	}
	if len(atomRepo.saved) != 2 {
		t.Errorf("claims = %d, want one per part", len(atomRepo.saved))
	}
	// Each claim cites its own part — no part's text was covered by another part's claim.
	for _, a := range atomRepo.saved {
		if len(a.Sources) != 1 || a.Sources[0].NodeID == "" {
			t.Errorf("claim %q has provenance %+v, want its own node", a.Claim, a.Sources)
		}
	}
	if got := strings.Count(logs.joined(), "part start"); got != 2 {
		t.Errorf("parts started = %d, want 2:\n%s", got, logs.joined())
	}
}

// TestDistillAuditsEveryPart: after a part's claims are written, the model reads the text AGAINST
// them and writes whatever no claim covers. That is the completeness check — "complete" is verified
// against the text, not inferred from a count of cited nodes.
func TestDistillAuditsEveryPart(t *testing.T) {
	long := strings.Repeat("C++ is fast and close to the hardware. It supports concurrency. ", 12)
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		textNode("1", "Why C++", long, 1),
	}
	gw, atomRepo, parts, logs := distillFixture(t, nodes, []interfaces.AgentResponse{
		claimsResponse(`{"branch":"cpp","claim":"C++ is fast and close to the hardware.","body":"b","nodes":["1"]}`),
		// The audit: it sees the claim and the text, and ANSWERS with what the first pass left out.
		claimsResponse(`{"branch":"cpp","claim":"C++ supports concurrency.","body":"b","nodes":["1"]}`),
	})

	if !anyPromptContains(gw, "Check ONE part") {
		t.Fatalf("the part was never audited; prompts:\n%s", strings.Join(promptsOf(gw), "\n---\n"))
	}
	if !anyPromptContains(gw, "C++ is fast and close to the hardware.") {
		t.Errorf("the audit does not show the claims that exist, so it cannot spot what is missing:\n%s",
			strings.Join(promptsOf(gw), "\n---\n"))
	}
	if len(atomRepo.saved) < 2 {
		t.Errorf("claims = %d, want the audit to add the missing statement", len(atomRepo.saved))
	}
	if st := parts.m["1"]; st.State != entities.PartDone || st.Atoms < 2 {
		t.Errorf("part state = %+v, want done with both claims", st)
	}
	if !logs.contains("passes=2") {
		t.Errorf("the log does not show that the part was written and then audited:\n%s", logs.joined())
	}
	// The live view needs the SOURCE's claim count, not the part's: the part's number would jump
	// around in the row while the run grows the source.
	if !logs.contains("source_atoms=2") {
		t.Errorf("the log line does not carry the source's total claim count:\n%s", logs.joined())
	}
}

// TestDistillContinuesAfterATruncatedAnswer: a turn cut off by the output limit is NOT the model
// finishing its work. The same document produced 44 claims in one run and 36 in another because the
// second wrote everything into one answer that hit the limit; the model then read its own results,
// saw every node cited once, and said "done". Nothing noticed, so the claims it never got to write
// were lost while every log line looked fine.
func TestDistillContinuesAfterATruncatedAnswer(t *testing.T) {
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		textNode("1", "Applications", "C++ is used in games and in browsers.", 1),
	}
	gw, atomRepo, parts, logs := distillFixture(t, nodes, []interfaces.AgentResponse{
		// The answer that hit the limit…
		// One claim is COMPLETE before the cut — salvage keeps it — and the rest is not.
		truncatedAnswer(`{"claims":[{"branch":"cpp","claim":"C++ is used in games.","body":"b","nodes":["1"]},{"branch":"cpp","claim":"C++ is used`),
		// …and the continuation ANSWERS with what was meant to be written.
		claimsResponse(`{"branch":"cpp","claim":"C++ is used in browsers.","body":"b","nodes":["1"]}`),
		// The audit then closes the part.
		completeResponse(),
	})

	if !anyPromptContains(gw, "CUT OFF") {
		t.Fatalf("a cut-off answer was accepted as finished; prompts:\n%s", strings.Join(promptsOf(gw), "\n---\n"))
	}
	if len(atomRepo.saved) < 2 {
		t.Errorf("claims = %d, want the continuation to add the missing ones", len(atomRepo.saved))
	}
	if !logs.contains("agent turn truncated") || !logs.contains("finish_reason=length") {
		t.Errorf("the log does not report that the answer was cut off:\n%s", logs.joined())
	}
	if st := parts.m["1"]; st.State != entities.PartDone {
		t.Errorf("part state = %v (%s), want done after the continuation", st.State, st.Reason)
	}
}

// TestDistillMarksACutOffPartIncomplete: when the endpoint keeps cutting the answer off, the part is
// NOT proven complete — it is recorded incomplete and derived again, instead of passing for done.
func TestDistillMarksACutOffPartIncomplete(t *testing.T) {
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		textNode("1", "Huge", strings.Repeat("A statement about C++. ", 40), 1),
	}
	cut := func(claim string) []interfaces.AgentResponse {
		return []interfaces.AgentResponse{
			truncatedAnswer(fmt.Sprintf(`{"claims":[{"branch":"cpp","claim":%q,"body":"b","nodes":["1"]}`, claim)),
		}
	}
	responses := append(append(append(cut("First statement."), cut("Second statement.")...), cut("Third statement.")...), cut("Fourth statement.")...)
	_, _, parts, logs := distillFixture(t, nodes, responses)

	st := parts.m["1"]
	if st.State != entities.PartIncomplete || st.Reason != "truncated" {
		t.Errorf("part state = %v (%s), want incomplete/truncated", st.State, st.Reason)
	}
	if !logs.contains("reason=truncated") {
		t.Errorf("the log does not name the truncation:\n%s", logs.joined())
	}
}

// TestDistillMarksAnAuditedNoClaimPartAsSourceRelative: a part that the model read and the audit
// declared complete, with no claims, is document metadata (an appendix intro, a surviving cover
// page). It must be recorded done as source_relative — retrying it would only produce the same
// non-claims, and the global barrier must not stay blocked on it.
func TestDistillMarksAnAuditedNoClaimPartAsSourceRelative(t *testing.T) {
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		textNode("1", "Boilerplate", "Accept cookies to continue reading.", 1),
	}
	atomRepo, parts, logs, err := runDistillErr(t, nodes, &fakeGateway{responses: []interfaces.AgentResponse{
		finalResponse(`{"claims":[]}`),
		finalResponse("COMPLETE"),
	}})
	if err != nil {
		t.Fatalf("a metadata part is a complete derivation, got %v", err)
	}
	if len(atomRepo.saved) != 0 {
		t.Errorf("claims = %d, want none", len(atomRepo.saved))
	}
	st := parts.m["1"]
	if st.State != entities.PartDone || st.Reason != "source_relative" {
		t.Errorf("part state = %v (%s), want done/source_relative", st.State, st.Reason)
	}
	if !logs.contains("reason=source_relative") {
		t.Errorf("a metadata part must be visible in the log:\n%s", logs.joined())
	}
}

// TestDistillKeepsAPartOpenWhenTheAuditDidNotConfirm: an empty first answer is not enough. If the
// audit does not say the part is complete, the part stays open and the run fails — the model may
// simply have failed to write.
func TestDistillKeepsAPartOpenWhenTheAuditDidNotConfirm(t *testing.T) {
	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Doc"},
		textNode("1", "Boilerplate", "Accept cookies to continue reading.", 1),
	}
	_, parts, _, err := runDistillErr(t, nodes, &fakeGateway{responses: []interfaces.AgentResponse{
		finalResponse(`{"claims":[]}`),
		finalResponse(`{"complete":false,"claims":[]}`),
	}})
	if err == nil {
		t.Error("a run whose audit did not confirm completion must fail")
	}
	st := parts.m["1"]
	if st.State != entities.PartIncomplete || st.Reason != "no_claims" {
		t.Errorf("part state = %v (%s), want incomplete/no_claims", st.State, st.Reason)
	}
}
