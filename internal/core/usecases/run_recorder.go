package usecases

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// StepRecorder writes and logs run_steps for a run, keeping a shared sequence
// across the phases of one run (distill then compile).
type StepRecorder struct {
	ctx  context.Context
	runs interfaces.RunRepository
	run  ulid.ULID
	log  *slog.Logger
	// mu guards seq: the page bodies of one cluster pass (and the parts of one document) run in
	// parallel, and a run_steps row per tool call must keep a sequence that is neither doubled nor
	// racy.
	mu  sync.Mutex
	seq int
}

// ID returns the run this recorder belongs to.
func (r *StepRecorder) ID() ulid.ULID { return r.run }

// NewStepRecorder returns a recorder for the given run. log may be nil.
func NewStepRecorder(ctx context.Context, runs interfaces.RunRepository, runID ulid.ULID, log *slog.Logger) *StepRecorder {
	return &StepRecorder{ctx: ctx, runs: runs, run: runID, log: log}
}

// OnStep records one tool call. It matches agents.Loop.OnStep.
func (r *StepRecorder) OnStep(info agents.StepInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	if r.log != nil {
		attrs := []any{
			"run", r.run.String(),
			"seq", r.seq,
			"tool", info.Tool,
			"dur", info.Duration.Round(time.Millisecond).String(),
			"result", agents.Digest(info.Result),
		}
		if info.Err != nil {
			r.log.Warn("agent step failed", append(attrs, "err", info.Err.Error())...)
		} else {
			r.log.Info("agent step", attrs...)
		}
	}
	_ = r.runs.AddStep(r.ctx, entities.RunStep{
		RunID:        r.run,
		Seq:          r.seq,
		Tool:         info.Tool,
		ArgsDigest:   agents.Digest(info.Arguments),
		ResultDigest: agents.Digest(info.Result),
		IsError:      info.Err != nil,
		DurationMS:   int(info.Duration.Milliseconds()),
	})
}

// turnLogger logs each LLM turn (the "model is thinking" gap), so a stalled call is
// visible in the terminal. It returns nil when log is nil.
func turnLogger(log *slog.Logger, phase string) func(int, interfaces.AgentResponse, error) {
	if log == nil {
		return nil
	}
	return func(iteration int, resp interfaces.AgentResponse, err error) {
		if err != nil {
			log.Warn("llm turn failed", "phase", phase, "iteration", iteration, "err", err.Error())
			return
		}
		log.Info("llm turn", "phase", phase, "iteration", iteration,
			"tool_calls", len(resp.Message.ToolCalls), "tokens", resp.TotalTokens)
	}
}

// logTurn records one model turn in the source's own log: iteration, which tools it asked
// for, and what it cost. Queries belong to a source's history just as much as the writes do.
func logTurn(
	ctx context.Context, src interfaces.SourceLog, clock interfaces.Clock,
	hash, phase, runID string, extra map[string]any, iteration int,
	resp interfaces.AgentResponse, err error,
) {
	if src == nil {
		return
	}
	fields := map[string]any{
		"iteration": iteration, "tool_calls": len(resp.Message.ToolCalls),
		"prompt_tokens": resp.PromptTokens, "completion_tokens": resp.CompletionTokens,
		"tokens": resp.TotalTokens,
	}
	maps.Copy(fields, extra)
	if resp.FinishReason != "" {
		fields["finish_reason"] = resp.FinishReason
	}
	level, msg := "info", "agent turn"
	if err != nil {
		level, msg = "error", "agent turn failed"
		fields["err"] = err
	} else if resp.Truncated() {
		// The output limit ended this answer, not the model: the claims it never got to write
		// are missing because the turn was cut off. Say so instead of looking complete.
		level, msg = "warn", "agent turn truncated"
		if names := toolNames(resp.Message.ToolCalls); names != "" {
			fields["tools"] = names
		}
	} else if names := toolNames(resp.Message.ToolCalls); names != "" {
		fields["tools"] = names
	} else if c := oneLine(resp.Message.Content, 120); c != "" {
		fields["reply"] = c
	}
	src.Append(ctx, hash, interfaces.SourceLogEvent{
		At: clock.Now(), RunID: runID, Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

// logToolCall records one tool call: which tool, with which arguments, what it answered and
// how long it took. A failed call is the reason a run produces nothing — it must be readable.
func logToolCall(
	ctx context.Context, src interfaces.SourceLog, clock interfaces.Clock,
	hash, phase, runID string, extra map[string]any, si agents.StepInfo,
) {
	if src == nil {
		return
	}
	fields := map[string]any{
		"tool": si.Tool, "dur": si.Duration.Round(time.Millisecond).String(),
		"args": oneLine(si.Arguments, 200),
	}
	maps.Copy(fields, extra)
	level, msg := "info", "tool call"
	if si.Err != nil {
		level, msg = "warn", "tool call failed"
		fields["err"] = si.Err
	} else {
		fields["result"] = oneLine(si.Result, 120)
		fields["result_bytes"] = len(si.Result)
	}
	src.Append(ctx, hash, interfaces.SourceLogEvent{
		At: clock.Now(), RunID: runID, Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

// chainTurns calls both turn hooks (the global log and the source log).
func chainTurns(hooks ...func(int, interfaces.AgentResponse, error)) func(int, interfaces.AgentResponse, error) {
	active := make([]func(int, interfaces.AgentResponse, error), 0, len(hooks))
	for _, h := range hooks {
		if h != nil {
			active = append(active, h)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(i int, resp interfaces.AgentResponse, err error) {
		for _, h := range active {
			h(i, resp, err)
		}
	}
}

// toolNames lists the tools a turn asked for.
func toolNames(calls []interfaces.AgentToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return strings.Join(names, ",")
}

// logRetry records that a transient model failure is being retried: the wait is part of the run's
// wall-clock, so it belongs in the source's own log rather than only in the process log.
func logRetry(
	ctx context.Context, src interfaces.SourceLog, clock interfaces.Clock,
	hash, phase, runID string, extra map[string]any,
) func(iteration, attempt int, wait time.Duration, err error) {
	if src == nil {
		return nil
	}
	return func(iteration, attempt int, wait time.Duration, err error) {
		fields := map[string]any{
			"iteration": iteration, "attempt": attempt, "wait": wait.String(), "err": err,
		}
		maps.Copy(fields, extra)
		src.Append(ctx, hash, interfaces.SourceLogEvent{
			At: clock.Now(), RunID: runID, Phase: phase, Level: "warn",
			Message: "model call failed, retrying", Fields: fields,
		})
	}
}

// retryNotice is the OnRetry shape a plain gateway call uses: the same line the loop writes, for
// the calls that are not part of a conversation (naming a document, deriving a taxonomy).
func retryNotice(
	ctx context.Context, src interfaces.SourceLog, clock interfaces.Clock, hash, phase, runID string,
) func(attempt int, wait time.Duration, err error) {
	if src == nil {
		return nil
	}
	return func(attempt int, wait time.Duration, err error) {
		src.Append(ctx, hash, interfaces.SourceLogEvent{
			At: clock.Now(), RunID: runID, Phase: phase, Level: "warn",
			Message: "model call failed, retrying",
			Fields:  map[string]any{"attempt": attempt, "wait": wait.String(), "err": err},
		})
	}
}
