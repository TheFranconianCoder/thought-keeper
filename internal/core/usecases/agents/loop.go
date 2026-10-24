// Package agents is the bounded tool-calling loop. It is core: it depends only on
// the AgentGateway port, never on a concrete LLM SDK.
package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Tool is a callable tool exposed to the model.
type Tool struct {
	Def interfaces.AgentToolDef
	Run func(ctx context.Context, args json.RawMessage) (string, error)
}

// StepInfo reports one executed tool call for the audit.
// Markers keep a message from ever being empty: a tool-calling turn and a tool that produced
// nothing both need *some* text for the request to be accepted.
const (
	toolCallMarker  = "(tool call)"
	emptyToolResult = "(no output)"
)

type StepInfo struct {
	Tool      string
	Arguments string
	Result    string
	Err       error
	Duration  time.Duration
}

// Usage accumulates token usage.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Result is the outcome of a run.
type Result struct {
	// Elided counts tool answers that were replaced by a note to keep the transcript bounded.
	Elided     int
	Content    string
	Iterations int
	Usage      Usage
	// FinishReason is the last turn's finish reason, and Truncated the answer to "was it cut off
	// by the output limit?" — a truncated run did NOT end because the model was done, so a caller
	// that reads "no tool calls" as "finished" would accept a short answer as a complete one.
	FinishReason string
	Truncated    bool
}

// Loop is the bounded tool-calling loop.
type Loop struct {
	Gateway interfaces.AgentGateway
	Model   string
	// ReasoningEffort is passed to the endpoint for every turn of this loop.
	ReasoningEffort string
	System          string
	Tools           []Tool
	MaxIterations   int
	OnStep          func(StepInfo)
	OnTurn          func(iteration int, resp interfaces.AgentResponse, err error)
	// OnRetry is called before a transient failure is retried, so the run's own log says that the
	// process waited instead of silently losing time (or dying).
	OnRetry func(iteration, attempt int, wait time.Duration, err error)

	// MaxToolResult caps one tool's answer; MaxHistoryChars caps how much tool output the
	// conversation carries at all. A transcript that grows with every answer makes each turn
	// more expensive than the work it does, and old answers are not needed any more: they were
	// consumed when they arrived.
	MaxToolResult   int
	MaxHistoryChars int
}

const (
	defaultMaxIterations = 8
	// One answer is capped so a single big listing cannot dominate the conversation.
	defaultMaxToolResult = 6000
	// Tool output kept verbatim across the conversation. Older answers stay in the transcript
	// as a one-line note: the tool ran, its data was used, and the ids/claims it produced are
	// in the messages that followed it.
	defaultMaxHistoryChars = 24000
	// A short line is not worth replacing with a note; the bookkeeping stays cheap and the
	// transcript keeps reading naturally.
	minElidableContent = 400
)

// maxToolResult returns the configured cap or the default.
func (l Loop) maxToolResult() int {
	if l.MaxToolResult > 0 {
		return l.MaxToolResult
	}
	return defaultMaxToolResult
}

// maxHistoryChars returns the configured transcript budget or the default.
func (l Loop) maxHistoryChars() int {
	if l.MaxHistoryChars > 0 {
		return l.MaxHistoryChars
	}
	return defaultMaxHistoryChars
}

// keepNewestAssistant is how many of the model's own turns keep their prose. The tool calls (and
// their ids, which the following answers must match) and the reasoning are never touched.
const keepNewestAssistant = 2

// elideAssistantProse drops the prose of old assistant turns. The tool RESULT policy is separate
// and stays budget-driven, because a tool answer is evidence the model may still need; its own
// commentary is not.
func elideAssistantProse(msgs []interfaces.AgentMessage, idx *[]int, keep int) int {
	count := 0
	for len(*idx) > keep {
		i := (*idx)[0]
		*idx = (*idx)[1:]
		if len(msgs[i].Content) < minElidableContent {
			continue
		}
		msgs[i].Content = elidedNote("assistant", len(msgs[i].Content))
		count++
	}
	return count
}

// elideOldResults replaces the oldest tool answer with a short note until the carried tool output
// fits the budget. It never touches the newest three, so the work at hand stays intact.
func elideOldResults(msgs []interfaces.AgentMessage, idx *[]int, chars *int, budget int) int {
	const keepNewest = 3
	count := 0
	for budget > 0 && len(*idx) > keepNewest && *chars > budget {
		i := (*idx)[0]
		*idx = (*idx)[1:]
		old := len(msgs[i].Content)
		name := msgs[i].Name
		if name == "" {
			name = "assistant"
		}
		msgs[i].Content = elidedNote(name, old)
		*chars -= old - len(msgs[i].Content)
		count++
	}
	return count
}

// elidedNote replaces a tool answer that is no longer needed.
func elidedNote(tool string, bytes int) string {
	return fmt.Sprintf("(result elided: %s returned %d bytes; it was used when it arrived)", tool, bytes)
}

// Run drives the conversation until the model answers without tool calls or the
// iteration bound is hit.
func (l Loop) Run(ctx context.Context, userMessage string) (Result, error) {
	defs := make([]interfaces.AgentToolDef, 0, len(l.Tools))
	for _, t := range l.Tools {
		defs = append(defs, t.Def)
	}

	var msgs []interfaces.AgentMessage
	if l.System != "" {
		msgs = append(msgs, interfaces.AgentMessage{Role: "system", Content: l.System})
	}
	msgs = append(msgs, interfaces.AgentMessage{Role: "user", Content: userMessage})

	max := l.MaxIterations
	if max <= 0 {
		max = defaultMaxIterations
	}

	var (
		usage         Usage
		toolMsgs      []int
		toolChars     int
		assistantMsgs []int
		elided        int
		finishWhy     string
		wasTruncated  bool
	)
	for i := 1; i <= max; i++ {
		call := interfaces.AgentRequest{
			Model: l.Model, Messages: msgs, Tools: defs, ReasoningEffort: l.ReasoningEffort,
		}
		if l.OnRetry != nil {
			iteration := i
			call.OnRetry = func(attempt int, wait time.Duration, err error) {
				l.OnRetry(iteration, attempt, wait, err)
			}
		}
		resp, err := l.Gateway.Complete(ctx, call)
		if l.OnTurn != nil {
			l.OnTurn(i, resp, err)
		}
		if err != nil {
			return Result{Iterations: i - 1, Usage: usage, Elided: elided, FinishReason: finishWhy, Truncated: wasTruncated}, err
		}
		usage.PromptTokens += resp.PromptTokens
		usage.CompletionTokens += resp.CompletionTokens
		usage.TotalTokens += resp.TotalTokens
		if resp.FinishReason != "" {
			finishWhy = resp.FinishReason
		}
		if resp.Truncated() {
			wasTruncated = true
		}
		// A turn that only calls tools carries no text, and some gateways reject a message
		// whose content is empty ("Message content must contain text"). Never append one.
		assistant := resp.Message
		if strings.TrimSpace(assistant.Content) == "" && len(assistant.ToolCalls) > 0 {
			assistant.Content = toolCallMarker
		}
		msgs = append(msgs, assistant)
		if len(assistant.Content) >= minElidableContent {
			assistantMsgs = append(assistantMsgs, len(msgs)-1)
		}

		if len(resp.Message.ToolCalls) == 0 {
			return Result{
				Content: resp.Message.Content, Iterations: i, Usage: usage, Elided: elided,
				FinishReason: finishWhy, Truncated: wasTruncated,
			}, nil
		}
		for _, call := range resp.Message.ToolCalls {
			start := time.Now()
			result, callErr := l.call(ctx, call)
			if l.OnStep != nil {
				l.OnStep(StepInfo{
					Tool:      call.Name,
					Arguments: call.Arguments,
					Result:    result,
					Err:       callErr,
					Duration:  time.Since(start),
				})
			}
			content := result
			if callErr != nil {
				content = "error: " + callErr.Error()
			}
			if strings.TrimSpace(content) == "" {
				content = emptyToolResult
			}
			if limit := l.maxToolResult(); limit > 0 && len(content) > limit {
				content = content[:limit] + fmt.Sprintf("\n… (truncated, %d bytes total)", len(content))
			}
			msgs = append(msgs, interfaces.AgentMessage{
				Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: content,
			})
			toolMsgs = append(toolMsgs, len(msgs)-1)
			toolChars += len(content)
		}
		// Keep the conversation bounded: the most recent answers stay, earlier ones become a
		// note. The tool_call/tool pairing is untouched, so the request stays valid.
		elided += elideOldResults(msgs, &toolMsgs, &toolChars, l.maxHistoryChars())
		// A turn's prose is a monologue, not evidence: it is dropped as soon as it is old. One real
		// run wrote 10 426 completion tokens into a single turn and every later request re-sent
		// them — the plan pass grew from 27 k to 52 k prompt tokens and burned its whole budget.
		elided += elideAssistantProse(msgs, &assistantMsgs, keepNewestAssistant)
	}
	// The tool budget is spent. One more turn WITHOUT tools: the model answers from what it has
	// already read instead of the run dying with nothing.
	//
	// Hitting the cap is not a bug — a hard question needs many lookups — but FAILING because of it
	// is: a partial answer that cites the pages it used is worth more than an error, and the run
	// reports that it was cut short (Truncated) so nobody mistakes it for a finished one.
	msgs = append(msgs, interfaces.AgentMessage{Role: "user", Content: wrapUpPrompt})
	final, err := l.Gateway.Complete(ctx, interfaces.AgentRequest{
		Model: l.Model, Messages: msgs, ReasoningEffort: l.ReasoningEffort,
	})
	if l.OnTurn != nil {
		l.OnTurn(max+1, final, err)
	}
	if err == nil {
		usage.PromptTokens += final.PromptTokens
		usage.CompletionTokens += final.CompletionTokens
		usage.TotalTokens += final.TotalTokens
		if answer := strings.TrimSpace(final.Message.Content); answer != "" {
			return Result{
				Content: answer, Iterations: max + 1, Usage: usage, Elided: elided,
				FinishReason: final.FinishReason, Truncated: true,
			}, nil
		}
	}
	return Result{
		Iterations: max, Usage: usage, Elided: elided, FinishReason: finishWhy, Truncated: wasTruncated,
	}, fmt.Errorf("max iterations (%d) reached", max)
}

// wrapUpPrompt is the last turn's instruction: answer now, with what is already in hand. It is
// deliberately blunt — a model that keeps wanting to look things up produces nothing.
const wrapUpPrompt = "The tool budget for this run is spent. Answer NOW, in prose, using only what you have " +
	"already read, and cite the pages you used as [[Page Title]]. Do not ask to call a tool again."

func (l Loop) call(ctx context.Context, call interfaces.AgentToolCall) (string, error) {
	for _, t := range l.Tools {
		if t.Def.Name != call.Name {
			continue
		}
		args := json.RawMessage(call.Arguments)
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		return t.Run(ctx, args)
	}
	return "", fmt.Errorf("unknown tool %q", call.Name)
}

// Digest returns a short stable digest of a string for the audit.
func Digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
