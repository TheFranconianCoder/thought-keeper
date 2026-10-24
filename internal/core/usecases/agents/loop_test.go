package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

type scriptedGateway struct {
	responses []interfaces.AgentResponse
	i         int
	requests  []interfaces.AgentRequest
}

func (g *scriptedGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.requests = append(g.requests, req)
	if g.i >= len(g.responses) {
		return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: "done"}}, nil
	}
	r := g.responses[g.i]
	g.i++
	return r, nil
}

// TestNoMessageIsEverEmpty: a tool-calling turn carries no text, and a tool may produce none
// either. Gateways reject a request whose messages contain an empty content ("Message content
// must contain text"), so the loop must never build one.
func TestNoMessageIsEverEmpty(t *testing.T) {
	gw := &scriptedGateway{responses: []interfaces.AgentResponse{
		// Turn 1: tools called, with NO text (exactly what a reasoning model returns).
		{Message: interfaces.AgentMessage{Role: "assistant", Content: "", ToolCalls: []interfaces.AgentToolCall{
			{ID: "c1", Name: "silent", Arguments: "{}"},
			{ID: "c2", Name: "loud", Arguments: "{}"},
		}}},
		{Message: interfaces.AgentMessage{Role: "assistant", Content: "done"}},
	}}
	loop := agents.Loop{
		Gateway: gw, Model: "test", System: "sys", MaxIterations: 4,
		Tools: []agents.Tool{
			{
				Def: interfaces.AgentToolDef{Name: "silent", Parameters: json.RawMessage(`{"type":"object"}`)},
				Run: func(context.Context, json.RawMessage) (string, error) { return "", nil },
			},
			{
				Def: interfaces.AgentToolDef{Name: "loud", Parameters: json.RawMessage(`{"type":"object"}`)},
				Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
			},
		},
	}
	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(gw.requests) < 2 {
		t.Fatalf("requests = %d, want at least 2", len(gw.requests))
	}
	for i, msg := range gw.requests[1].Messages {
		if msg.Content == "" {
			t.Errorf("message %d (%s) has empty content", i, msg.Role)
		}
	}
}

// TestTranscriptStaysBounded: the conversation must not grow with every tool answer. Old
// answers become a note, the newest stay, and the tool_call/tool pairing stays intact so the
// request keeps being valid.
func TestTranscriptStaysBounded(t *testing.T) {
	big := strings.Repeat("x", 5000) // one answer per turn, 5 KB
	var responses []interfaces.AgentResponse
	for i := range 12 {
		responses = append(responses, interfaces.AgentResponse{
			Message: interfaces.AgentMessage{Role: "assistant", ToolCalls: []interfaces.AgentToolCall{
				{ID: fmt.Sprintf("c%d", i), Name: "huge", Arguments: "{}"},
			}},
		})
	}
	responses = append(responses, interfaces.AgentResponse{
		Message: interfaces.AgentMessage{Role: "assistant", Content: "done"},
	})
	gw := &scriptedGateway{responses: responses}
	loop := agents.Loop{
		Gateway: gw, Model: "test", System: "sys", MaxIterations: 20,
		Tools: []agents.Tool{{
			Def: interfaces.AgentToolDef{Name: "huge", Parameters: json.RawMessage(`{"type":"object"}`)},
			Run: func(context.Context, json.RawMessage) (string, error) { return big, nil },
		}},
	}
	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	last := gw.requests[len(gw.requests)-1]
	carried := 0
	elided := 0
	for _, m := range last.Messages {
		if m.Role == "tool" {
			carried += len(m.Content)
			if strings.Contains(m.Content, "result elided") {
				elided++
			}
		}
	}
	if carried > 24000+6000 {
		t.Errorf("tool output carried = %d bytes, want it bounded", carried)
	}
	if elided == 0 {
		t.Error("nothing was elided; the transcript would keep growing")
	}
	// Every assistant tool call must still have its tool answer.
	answered := map[string]bool{}
	for _, m := range last.Messages {
		if m.Role == "tool" {
			answered[m.ToolCallID] = true
		}
	}
	for _, m := range last.Messages {
		for _, c := range m.ToolCalls {
			if !answered[c.ID] {
				t.Errorf("tool call %s lost its answer", c.ID)
			}
		}
	}
}

// retryNotifyingGateway reports a retry the way the adapter does — through the request — and then
// answers. The loop must pass that notice on, so the retry shows up in the run's own log.
type retryNotifyingGateway struct{ seen int }

func (g *retryNotifyingGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	if req.OnRetry != nil {
		g.seen++
		req.OnRetry(1, time.Second, errors.New("timeout"))
	}
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: "done"}}, nil
}

// TestLoopReportsTheRetriesItsCallsNeeded: the loop does not retry itself (the gateway does, so that
// EVERY call is covered), but it must hand the notice to its own logger — otherwise a run that
// waited a minute for a slow endpoint looks like it was idle.
func TestLoopReportsTheRetriesItsCallsNeeded(t *testing.T) {
	gw := &retryNotifyingGateway{}
	var gotIteration, gotAttempt int
	loop := agents.Loop{
		Gateway: gw, Model: "test", System: "sys", MaxIterations: 4,
		OnRetry: func(iteration, attempt int, _ time.Duration, err error) {
			gotIteration, gotAttempt = iteration, attempt
			if err == nil {
				t.Error("OnRetry was called without an error")
			}
		},
	}
	res, err := loop.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Content != "done" || gw.seen != 1 {
		t.Fatalf("result = %q, notices = %d", res.Content, gw.seen)
	}
	if gotIteration != 1 || gotAttempt != 1 {
		t.Errorf("OnRetry(iteration=%d, attempt=%d), want (1, 1)", gotIteration, gotAttempt)
	}
}

// verboseGateway answers with a long monologue before every tool call — exactly what one real
// compile turn did (10 426 completion tokens) when it should have been planning.
type verboseGateway struct {
	turns   int
	replies []string
}

func (g *verboseGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.turns++
	g.replies = append(g.replies, req.Messages[len(req.Messages)-1].Content)
	// The whole conversation as ONE string: the test measures what the loop keeps carrying.
	var carried strings.Builder
	for _, m := range req.Messages {
		carried.WriteString(m.Content)
	}
	g.replies = append(g.replies, carried.String())
	if g.turns >= 6 {
		return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: "done"}}, nil
	}
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{
		Role: "assistant", Content: strings.Repeat("deliberating at length. ", 400),
		ToolCalls: []interfaces.AgentToolCall{{ID: "c", Name: "noop", Arguments: "{}"}},
	}}, nil
}

// TestLoopElidesTheModelsOwnProse: a long monologue must not be re-sent forever. Carrying it made a
// real plan pass grow from 27 k to 52 k prompt tokens and die on the run timeout.
func TestLoopElidesTheModelsOwnProse(t *testing.T) {
	gw := &verboseGateway{}
	loop := agents.Loop{
		Gateway: gw, Model: "test", System: "sys", MaxIterations: 8,
		Tools: []agents.Tool{{
			Def: interfaces.AgentToolDef{
				Name: "noop", Description: "does nothing",
				Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
		}},
	}
	res, err := loop.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Elided == 0 {
		t.Error("nothing was elided — the monologue is carried forever")
	}
	// The last request must not carry all six monologues.
	last := gw.replies[len(gw.replies)-1]
	if n := strings.Count(last, "deliberating at length."); n > 2*400 {
		t.Errorf("the last request still carries %d monologue sentences", n)
	}
	if !strings.Contains(last, "elided") {
		t.Errorf("the elision is not visible in the transcript:\n%s", last[:min(len(last), 400)])
	}
}
