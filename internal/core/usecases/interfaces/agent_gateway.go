package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrTransient marks a failure that is worth RETRYING: a timeout, a dropped connection, a 429 or
// a 5xx. A wrong API key, a bad request or a model the endpoint does not serve is NOT transient —
// retrying those only burns quota. The classification lives in the adapter (it knows the SDK's
// error shapes); the core only asks the question, so it stays free of the SDK.
var ErrTransient = errors.New("agent: transient failure")

// Transient wraps an error as retryable.
func Transient(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrTransient, err)
}

// IsTransient reports whether an error is worth another attempt.
func IsTransient(err error) bool { return errors.Is(err, ErrTransient) }

// ErrModelWindow marks a DERIVATION call the app refused because its configured model windows are
// closed. It is not a failure of the endpoint: the work is deferred and resumes when the window
// closes, so the caller must not count it as a retryable error.
var ErrModelWindow = errors.New("agent: the derivation's model windows are closed")

// WindowClosedError says which instant opens the window again.
type WindowClosedError struct {
	Reopen time.Time
}

func (e WindowClosedError) Error() string {
	return fmt.Sprintf("%s until %s", ErrModelWindow, e.Reopen.UTC().Format(time.RFC3339))
}

// Is makes errors.Is(err, ErrModelWindow) true, so a use case can tell a deferral from a failure
// however the error was wrapped.
func (e WindowClosedError) Is(target error) bool { return target == ErrModelWindow }

// WindowClosed builds the deferral error for an instant the window opens again.
func WindowClosed(reopen time.Time) error { return WindowClosedError{Reopen: reopen} }

// IsWindowClosed reports whether err is the model-window refusal.
func IsWindowClosed(err error) bool { return errors.Is(err, ErrModelWindow) }

// WindowReopen returns when the derivation may call the model again.
func WindowReopen(err error) (time.Time, bool) {
	if closed, ok := errors.AsType[WindowClosedError](err); ok {
		return closed.Reopen, true
	}
	return time.Time{}, false
}

// AgentMessage is one message in an agent conversation.
type AgentMessage struct {
	Role       string // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []AgentToolCall // assistant
	ToolCallID string          // tool
	Name       string          // tool; core-only, never sent to the model
	// ReasoningContent is a thinking model's reasoning for this turn. It must be passed back
	// with the message it belongs to on the next request — the API rejects a thinking-model
	// conversation whose assistant turns lost it.
	ReasoningContent string // assistant
}

// AgentToolCall is a tool invocation requested by the model.
type AgentToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON
}

// AgentToolDef declares a tool to the model.
type AgentToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// AgentRequest is one completion request.
type AgentRequest struct {
	// ReasoningEffort controls how much a thinking model deliberates ("low", "medium", "high";
	// empty = the endpoint's default). The cheap steps (distill, compile) do not need deep
	// reasoning: reasoning tokens ARE the wall-clock time.
	ReasoningEffort string
	Model           string
	Messages        []AgentMessage
	Tools           []AgentToolDef
	// OnRetry is called before a transient failure is handed to the caller again. It travels with
	// the request so the retry is visible where the call was made (the source's own log), not only
	// in the process log — the gateway cannot know which document is being worked on.
	OnRetry func(attempt int, wait time.Duration, err error)
}

// AgentResponse is one completion response.
type AgentResponse struct {
	Message AgentMessage
	// FinishReason is how the endpoint ended the answer: "stop" (the model was done),
	// "tool_calls" (it asked for tools) or "length" (the OUTPUT LIMIT cut it off). "length" is
	// the difference between "the model finished" and "the model was interrupted" — treating the
	// two alike silently accepts a short answer, which is how a source ends up with too few
	// claims while every log line looks fine.
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Truncated reports whether the endpoint cut the answer off at its output limit.
func (r AgentResponse) Truncated() bool { return r.FinishReason == FinishReasonLength }

// FinishReasonLength is the finish reason of an answer that hit the output limit.
const FinishReasonLength = "length"

// AgentGateway is the LLM port. The core never imports the OpenAI SDK.
type AgentGateway interface {
	Complete(ctx context.Context, req AgentRequest) (AgentResponse, error)
}

// RetryPolicy says how often a transient model failure is retried and how long the first wait is.
// It is a policy, not an implementation: the loop applies it, the adapter classifies the error.
type RetryPolicy struct {
	Attempts int
	Base     time.Duration
	// Budget caps the WAIT of the whole ladder (not one attempt). Without it, attempts × timeout
	// is the real worst case — four attempts against a 300 s ceiling is twenty minutes for one
	// call, which behind a serialised worker looks exactly like a hang.
	Budget time.Duration
}
