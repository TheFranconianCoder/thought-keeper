package gateways

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
	"github.com/TheFranconianCoder/thought-keeper/internal/version"
)

// OpenAIConfig configures the OpenAI-compatible gateway.
type OpenAIConfig struct {
	// Retry is the policy for a transient failure (timeout, 429, 5xx).
	Retry   interfaces.RetryPolicy
	BaseURL string
	APIKey  string
	Timeout time.Duration
	Headers map[string]string
	// MaxConcurrent caps the calls in flight across the whole process. The pipeline fans out over
	// runs and parts; without a ceiling a rate-limited endpoint is hammered by more requests than
	// it refuses, and every refused turn is a part derived again. Zero means no ceiling.
	MaxConcurrent int
	// Log receives a warning when the endpoint rejects reasoning_effort and the call is
	// repeated without it.
	Log *slog.Logger
}

// OpenAIAgent is the AgentGateway backed by the native OpenAI SDK.
type OpenAIAgent struct {
	client *openai.Client
	log    *slog.Logger
	retry  interfaces.RetryPolicy
	slots  chan struct{}
}

// headerTransport injects headers that the endpoint requires (e.g. the opencode
// Zen proxy needs x-opencode-session).
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	return t.base.RoundTrip(req)
}

// resolveHeaders merges the user headers with a default User-Agent and always adds
// a per-run x-opencode-session. That header is a routing hint required by the
// opencode Zen proxy and ignored by other endpoints; a user value wins.
func resolveHeaders(_ string, extra map[string]string) map[string]string {
	h := map[string]string{"User-Agent": "thought-keeper/" + version.String()}
	maps.Copy(h, extra)
	if _, ok := h["x-opencode-session"]; !ok {
		h["x-opencode-session"] = randomSessionID()
	}
	return h
}

func randomSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("tk-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// NewOpenAIAgent builds the gateway. It returns false when no API key is set, so
// the app can degrade gracefully.
func NewOpenAIAgent(cfg OpenAIConfig) (*OpenAIAgent, bool) {
	if cfg.APIKey == "" {
		return nil, false
	}
	oc := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		oc.BaseURL = cfg.BaseURL
	}
	client := &http.Client{
		Transport: &headerTransport{
			base:    &reasoningTransport{base: http.DefaultTransport},
			headers: resolveHeaders(cfg.BaseURL, cfg.Headers),
		},
	}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}
	oc.HTTPClient = client
	retry := cfg.Retry
	if retry.Attempts <= 0 {
		retry.Attempts = 3
	}
	if retry.Base <= 0 {
		retry.Base = 2 * time.Second
	}
	a := &OpenAIAgent{client: openai.NewClientWithConfig(oc), log: cfg.Log, retry: retry}
	if cfg.MaxConcurrent > 0 {
		a.slots = make(chan struct{}, cfg.MaxConcurrent)
	}
	return a, true
}

// rejectedReasoningEffort reports whether an error is about the reasoning_effort parameter.
func rejectedReasoningEffort(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "reasoning_effort")
}

// Complete performs one chat completion with tool definitions.
func (a *OpenAIAgent) Complete(ctx context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		content := m.Content
		// Belt and braces: a request is rejected when any message has no text
		// ("Message content must contain text"), so nothing empty leaves this adapter.
		if strings.TrimSpace(content) == "" {
			switch {
			case m.Role == "tool":
				content = "(no output)"
			case len(m.ToolCalls) > 0:
				content = "(tool call)"
			default:
				content = "(empty)"
			}
		}
		// The name of a tool answer stays in the core (it words the elided note); on the wire the
		// tool_call_id alone ties the answer to its call, and Anthropic-Messages endpoints reject name.
		om := openai.ChatCompletionMessage{
			Role:             m.Role,
			Content:          content,
			ToolCallID:       m.ToolCallID,
			ReasoningContent: m.ReasoningContent,
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, openai.ToolCall{
				ID:   tc.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		msgs = append(msgs, om)
	}

	tools := make([]openai.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	chatReq := openai.ChatCompletionRequest{
		Model:           req.Model,
		Messages:        msgs,
		Tools:           tools,
		ReasoningEffort: req.ReasoningEffort,
	}
	resp, err := a.complete(ctx, req, chatReq)
	if err != nil && chatReq.ReasoningEffort != "" && rejectedReasoningEffort(err) {
		// An endpoint that does not know the knob must not break the run: ask again without it.
		if a.log != nil {
			a.log.Warn("endpoint rejected reasoning_effort, retrying without it",
				"effort", chatReq.ReasoningEffort, "err", err)
		}
		chatReq.ReasoningEffort = ""
		resp, err = a.complete(ctx, req, chatReq)
	}
	if err != nil {
		if transient(err) {
			// The caller decides how often to retry: a timeout or a 5xx is not the end of a run.
			return interfaces.AgentResponse{}, interfaces.Transient(fmt.Errorf("openai: %w", err))
		}
		return interfaces.AgentResponse{}, fmt.Errorf("openai: %w", err)
	}
	if len(resp.Choices) == 0 {
		return interfaces.AgentResponse{}, types.New("agent_error", "model returned no choices", types.ErrInternal)
	}

	choice := resp.Choices[0].Message
	out := interfaces.AgentResponse{
		Message: interfaces.AgentMessage{
			Role: "assistant", Content: choice.Content, ReasoningContent: choice.ReasoningContent,
		},
		FinishReason:     string(resp.Choices[0].FinishReason),
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	for _, tc := range choice.ToolCalls {
		out.Message.ToolCalls = append(out.Message.ToolCalls, interfaces.AgentToolCall{
			ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return out, nil
}

// complete sends one request and retries a TRANSIENT failure with a doubling backoff. Every model
// call in the process goes through here — the tree title, the taxonomy, queries, every agent turn —
// so a timeout or a 5xx is never a dead end, no matter which phase made the call. A wrong key or a
// bad request is handed straight back: retrying those only burns quota.
//
// A RATE LIMIT is treated differently from a timeout: the endpoint is telling us when it will be
// ready, so the ladder is longer and each step is at least a few seconds, with jitter so the parallel
// parts do not all retry in the same instant. Measured on one morning batch: 2,497 retries, 1,004
// turns that ended failed and 1,007 parts derived a second time — all of it an avoidable 12.9 % of
// the tokens.
func (a *OpenAIAgent) complete(ctx context.Context, req interfaces.AgentRequest, chatReq openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	attempts := a.retry.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	base := a.retry.Base
	if base <= 0 {
		base = 2 * time.Second
	}
	spent := time.Duration(0)
	for attempt := 1; ; attempt++ {
		release, slotErr := a.acquire(ctx)
		if slotErr != nil {
			return openai.ChatCompletionResponse{}, slotErr
		}
		resp, err := a.client.CreateChatCompletion(ctx, chatReq)
		release()
		if err == nil {
			return resp, nil
		}
		limit := attempts
		if rateLimited(err) {
			// A 429 is not a broken endpoint: it is a queue. Two more turns of patience, and never
			// shorter than the ladder below.
			limit = attempts + 2
		}
		if attempt > limit || !transient(err) {
			return resp, err
		}
		wait := backoffFor(base, attempt, rateLimited(err))
		if budget := a.retry.Budget; budget > 0 && spent+wait > budget {
			if a.log != nil {
				a.log.Warn("model call failed, retry budget used up", "attempt", attempt, "spent", spent.String(), "err", err)
			}
			return resp, err
		}
		spent += wait
		if a.log != nil {
			a.log.Warn("model call failed, retrying",
				"attempt", attempt, "of", limit, "wait", wait.String(), "err", err)
		}
		if req.OnRetry != nil {
			req.OnRetry(attempt, wait, err)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return openai.ChatCompletionResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// acquire takes one of the in-flight slots (a no-op without a ceiling).
func (a *OpenAIAgent) acquire(ctx context.Context) (func(), error) {
	if a.slots == nil {
		return func() {}, nil
	}
	select {
	case a.slots <- struct{}{}:
		return func() { <-a.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// backoffFor is the wait before the next attempt: doubling, never shorter than five seconds for a
// rate limit, and jittered by ±20 % so a fan-out of parallel parts does not retry in lockstep.
func backoffFor(base time.Duration, attempt int, limited bool) time.Duration {
	wait := base * time.Duration(1<<(attempt-1))
	if limited && wait < 5*time.Second {
		wait = 5 * time.Second
	}
	// rand/v2 is seeded per process by the runtime; no seeding, no shared state.
	jitter := 0.8 + 0.4*mrand.Float64()
	return time.Duration(float64(wait) * jitter)
}

// rateLimited reports a 429, which deserves the longer ladder.
func rateLimited(err error) bool {
	if apiErr, ok := errors.AsType[*openai.APIError](err); ok {
		return apiErr.HTTPStatusCode == http.StatusTooManyRequests
	}
	if reqErr, ok := errors.AsType[*openai.RequestError](err); ok {
		return reqErr.HTTPStatusCode == http.StatusTooManyRequests
	}
	return strings.Contains(err.Error(), "429")
}

// transient reports whether an error is worth another attempt. A timeout, a reset connection, an
// EOF, a rate limit (429) or a server error (5xx) are; a rejected key, a malformed request or an
// unknown model are not.
func transient(err error) bool {
	if err == nil {
		return false
	}
	// The client wraps transport failures; a timeout anywhere in the chain counts.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if apiErr, ok := errors.AsType[*openai.APIError](err); ok {
		return apiErr.HTTPStatusCode == http.StatusTooManyRequests || apiErr.HTTPStatusCode >= 500
	}
	if reqErr, ok := errors.AsType[*openai.RequestError](err); ok {
		if reqErr.HTTPStatusCode == http.StatusTooManyRequests || reqErr.HTTPStatusCode >= 500 {
			return true
		}
		// A connection that died mid-request carries no status at all.
		msg := reqErr.Error()
		for _, hint := range []string{"connection reset", "broken pipe", "unexpected EOF", "EOF", "TLS handshake timeout", "connection refused"} {
			if strings.Contains(msg, hint) {
				return true
			}
		}
	}
	for _, hint := range []string{"connection reset by peer", "unexpected EOF", "i/o timeout"} {
		if strings.Contains(err.Error(), hint) {
			return true
		}
	}
	return false
}
