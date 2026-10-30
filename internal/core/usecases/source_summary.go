package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// SourceSummary writes a source's OVERVIEW: the prose the detail view shows on its Summary tab.
//
// The summary is DERIVED, like the outline: it is written from the claims (and the outline's shape),
// never from the source's raw text, so it cannot say more than the vault actually knows. It is a
// sidecar (<hash>.summary.md), so a missing one is a visible file the consistency job can fill in and
// deleting the source deletes it with everything else.
//
// The summary follows a COMPLETE derivation. A source whose parts are still open would get an
// overview of a moving set of claims, so the run refuses until distill is `ok`; a full reset removes
// the old file, and the next complete distill writes a new one.
type SourceSummary struct {
	Index interfaces.IndexRepository
	Atoms interfaces.AtomRepository
	Trees interfaces.TreeStore
	Store interfaces.SourceStore
	// Gateway is the model; a source with claims needs it. A source that produced NO claims still
	// gets a one-line summary, without a model call: that is a fact, not an overview to invent.
	Gateway interfaces.AgentGateway
	Model   string
	// Language is the language the overview is written in (ISO 639-1, empty = English).
	Language    string
	Runs        interfaces.RunRepository
	SourceLog   interfaces.SourceLog
	Clock       interfaces.Clock
	Log         *slog.Logger
	MaxClaims   int
	MaxChars    int
	CallTimeout time.Duration
}

// SourceSummaryResult reports what the run wrote.
type SourceSummaryResult struct {
	Hash   string
	Claims int
	Chars  int
	Tokens int
}

// Execute writes the summary of one source and finishes its run row.
func (uc SourceSummary) Execute(ctx context.Context, runID ulid.ULID, hash string) (SourceSummaryResult, error) {
	res := SourceSummaryResult{Hash: hash}
	run := entities.Run{
		ID: runID, Op: entities.RunSummary, Model: uc.Model, TargetID: hash,
		Status: entities.RunRunning, StartedAt: uc.Clock.Now(),
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return res, uc.fail(ctx, run, "the source could not be read: "+err.Error())
	}
	if src.Bookmark {
		return res, uc.fail(ctx, run, "a bookmark has no derivation, so it has no summary")
	}
	if state := src.Steps.State(entities.StepDistill); state != entities.StepOK {
		return res, uc.fail(ctx, run, fmt.Sprintf(
			"the derivation is not finished (distill is %q), so there is nothing to summarise yet", state))
	}

	atoms, err := uc.Atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		return res, uc.fail(ctx, run, "the claims could not be read: "+err.Error())
	}
	res.Claims = len(atoms)

	body := ""
	if len(atoms) == 0 {
		// A document of metadata (a cover, a table of contents) correctly derives no claim. Saying so
		// is the honest summary — and it stops the consistency job from asking forever.
		body = agents.NoClaimsSummary(uc.Language)
	} else {
		if uc.Gateway == nil || uc.Model == "" {
			return res, uc.fail(ctx, run, "no model is configured")
		}
		var nodes []entities.SourceNode
		if uc.Trees != nil {
			// An outline that cannot be read is not a reason to refuse the summary: the claims alone
			// are enough, the outline only gives the shape.
			if read, tErr := uc.Trees.Read(ctx, hash); tErr == nil {
				nodes = read
			}
		}
		timeout := uc.CallTimeout
		if timeout <= 0 {
			timeout = 3 * time.Minute
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		resp, cErr := uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
			Model:           uc.Model,
			ReasoningEffort: "minimal",
			Messages: []interfaces.AgentMessage{
				{Role: "system", Content: agents.SummarySystemFor(uc.Language)},
				{Role: "user", Content: agents.SourceSummaryUser(
					src.Title, src.Language, uc.Language, summaryOutline(nodes), uc.claimLines(atoms),
				)},
			},
			OnRetry: retryNotice(ctx, uc.SourceLog, uc.Clock, hash, "summary", runID.String()),
		})
		if cErr != nil {
			return res, uc.fail(ctx, run, "the model call failed: "+cErr.Error())
		}
		res.Tokens = resp.TotalTokens
		run.PromptTokens, run.CompletionTokens, run.TotalTokens = resp.PromptTokens, resp.CompletionTokens, resp.TotalTokens
		if resp.Truncated() {
			return res, uc.fail(ctx, run, "the answer hit the output limit; the summary was not written")
		}
		body = stripCodeFence(resp.Message.Content)
		if body == "" {
			return res, uc.fail(ctx, run, "the model returned an empty summary")
		}
	}

	if err := uc.Store.PutRendered(hash, entities.SidecarSummary, []byte(body+"\n")); err != nil {
		return res, uc.fail(ctx, run, "the summary could not be written: "+err.Error())
	}
	res.Chars = len(body)

	run.Answer = oneLine(body, 300)
	run.Status = entities.RunSucceeded
	run.EndedAt = uc.Clock.Now()
	if uc.Runs != nil {
		if fErr := uc.Runs.FinishRun(ctx, run); fErr != nil && uc.Log != nil {
			uc.Log.Warn("summary: the run row could not be finished", "hash", hash, "run", runID.String(), "err", fErr)
		}
	}
	uc.log(ctx, hash, runID, "info", "summary written", map[string]any{
		"claims": res.Claims, "chars": res.Chars, "tokens": res.Tokens,
	})
	if uc.Log != nil {
		uc.Log.Info("source summary written", "hash", hash, "run", runID.String(),
			"claims", res.Claims, "chars", res.Chars, "tokens", res.Tokens)
	}
	return res, nil
}

// claimLines renders the claims the summary may use: the claim itself, its topic and its body. The
// cap is a prompt-size guard, not a ranking — a book with 600 claims is summarised from a broad,
// document-ordered slice, and the through-line is what the prompt asks for.
func (uc SourceSummary) claimLines(atoms []entities.Atom) string {
	maxClaims := uc.MaxClaims
	if maxClaims <= 0 {
		maxClaims = 250
	}
	maxChars := uc.MaxChars
	if maxChars <= 0 {
		maxChars = 36 << 10
	}
	var b strings.Builder
	for i, a := range atoms {
		if i >= maxClaims || b.Len() >= maxChars {
			break
		}
		topic := a.Topic
		if topic == "" {
			topic = "uncategorised"
		}
		fmt.Fprintf(&b, "- [%s] %s", topic, oneLine(a.Claim, 300))
		if body := oneLine(a.Body, 400); body != "" && body != oneLine(a.Claim, 300) {
			fmt.Fprintf(&b, " — %s", body)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// summaryOutline is the source's structure, titles only: it tells the model the shape of the
// document, which the flat claim list cannot. It is not knowledge of its own.
func summaryOutline(nodes []entities.SourceNode) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.NodeID == "0" || b.Len() >= 8<<10 {
			continue
		}
		title := strings.TrimSpace(n.Title)
		if title == "" {
			title = strings.TrimSpace(n.Summary)
		}
		if title == "" {
			continue
		}
		indent := strings.Repeat("  ", min(max(n.Depth, 0), 3))
		fmt.Fprintf(&b, "%s- %s\n", indent, oneLine(title, 120))
	}
	return b.String()
}

// stripCodeFence removes a fence a model added around prose it was told to write as Markdown.
func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[i+1:]
	} else {
		return ""
	}
	t = strings.TrimSpace(t)
	t = strings.TrimSuffix(t, "```")
	return strings.TrimSpace(t)
}

// fail records the failed run and returns the error. The caller (the worker) publishes it; the row is
// finished here so a failed summary never hangs as "running".
func (uc SourceSummary) fail(ctx context.Context, run entities.Run, msg string) error {
	run.Status = entities.RunFailed
	run.Error = msg
	run.EndedAt = uc.Clock.Now()
	if uc.Runs != nil {
		if fErr := uc.Runs.FinishRun(ctx, run); fErr != nil && uc.Log != nil {
			uc.Log.Warn("summary: the failed run could not be recorded", "hash", run.TargetID, "run", run.ID.String(), "err", fErr)
		}
	}
	uc.log(ctx, run.TargetID, run.ID, "error", "summary failed", map[string]any{"err": msg})
	if uc.Log != nil {
		uc.Log.Warn("source summary failed", "hash", run.TargetID, "run", run.ID.String(), "err", msg)
	}
	return errors.New(msg)
}

func (uc SourceSummary) log(ctx context.Context, hash string, runID ulid.ULID, level, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), RunID: runID.String(), Phase: "summary", Level: level, Message: msg, Fields: fields,
	})
}
