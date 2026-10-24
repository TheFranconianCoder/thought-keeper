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
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// TagSource derives a source's AUTO tags from its claims: the coarse shelves a reader files and filters
// the Sources list by. It is the source-side twin of TagWikiPage: the agent proposes, the app writes,
// and the human's own tags stay untouched and authoritative.
type TagSource struct {
	Index   interfaces.IndexRepository
	Atoms   interfaces.AtomRepository
	Tags    interfaces.SourceTagStore
	Gateway interfaces.AgentGateway
	Model   string
	// Vocab supplies the canonical source-tag names and the aliases a curation absorbed.
	Vocab     interfaces.VocabularyStore
	Runs      interfaces.RunRepository
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	Log       *slog.Logger
	Max       int
}

// TagSourceResult reports what the derivation produced.
type TagSourceResult struct {
	Hash   string
	Tags   []string
	Claims int
	Tokens int
}

// maxSourceTagSample bounds what the model sees. The claims are already compact; 6 KiB is the same
// ceiling a wiki page's tag sample uses.
const maxSourceTagSample = 6000

// Execute derives and stores one source's auto tags and finishes its run row.
func (uc TagSource) Execute(ctx context.Context, runID ulid.ULID, hash string) (TagSourceResult, error) {
	res := TagSourceResult{Hash: hash}
	run := entities.Run{
		ID: runID, Op: entities.RunTag, Model: uc.Model, TargetID: hash,
		Status: entities.RunRunning, StartedAt: uc.Clock.Now(),
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return res, uc.fail(ctx, run, "the source could not be read: "+err.Error())
	}
	if src.Bookmark {
		return res, uc.fail(ctx, run, "a bookmark has no derivation, so it has no auto tags")
	}
	if state := src.Steps.State(entities.StepDistill); state != entities.StepOK {
		return res, uc.fail(ctx, run, fmt.Sprintf(
			"the derivation is not finished (distill is %q), so there are no claims to tag yet", state))
	}
	atoms, err := uc.Atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		return res, uc.fail(ctx, run, "the claims could not be read: "+err.Error())
	}
	res.Claims = len(atoms)

	// A source that produced no claims gets the timestamp with an empty set, so the backfill does not
	// ask forever; there is nothing to label.
	tags := []string{}
	if len(atoms) > 0 {
		if uc.Gateway == nil || uc.Model == "" {
			return res, uc.fail(ctx, run, "no model is configured")
		}
		known, absorbed := sourceTagVocabulary(uc.Vocab)
		sample := sourceTagSample(atoms)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		resp, cErr := uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
			Model:           uc.Model,
			ReasoningEffort: "minimal",
			Messages: []interfaces.AgentMessage{
				{Role: "system", Content: agents.SourceTagsSystem},
				{Role: "user", Content: agents.SourceTagsUser(src.Title, sample, strings.Join(known, "\n"))},
			},
		})
		if cErr != nil {
			return res, uc.fail(ctx, run, "the model call failed: "+cErr.Error())
		}
		res.Tokens = resp.TotalTokens
		run.TotalTokens = resp.TotalTokens
		tags = normalizeSourceAutoTags(parseTags(resp.Message.Content), uc.Max, absorbed)
	}

	if err := uc.Tags.SetSourceAutoTags(ctx, hash, tags, uc.Clock.Now()); err != nil {
		return res, uc.fail(ctx, run, "the auto tags could not be written: "+err.Error())
	}
	res.Tags = tags

	run.Answer = strings.Join(tags, ", ")
	run.Status = entities.RunSucceeded
	run.EndedAt = uc.Clock.Now()
	if uc.Runs != nil {
		if fErr := uc.Runs.FinishRun(ctx, run); fErr != nil && uc.Log != nil {
			uc.Log.Warn("source tags: the run row could not be finished", "hash", hash, "run", runID.String(), "err", fErr)
		}
	}
	uc.log(ctx, hash, runID, "info", "auto tags derived", map[string]any{
		"tags": len(tags), "claims": res.Claims, "tokens": res.Tokens,
	})
	if uc.Log != nil {
		uc.Log.Info("source auto tags derived", "hash", hash, "run", runID.String(),
			"tags", len(tags), "claims", res.Claims, "tokens", res.Tokens)
	}
	return res, nil
}

// sourceTagVocabulary reads the canonical names and the alias table of the source-tags namespace.
func sourceTagVocabulary(store interfaces.VocabularyStore) (known []string, absorbed map[string]string) {
	if store == nil {
		return nil, nil
	}
	v, err := store.Read(entities.VocabularySourceTags)
	if err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, name := range v.Canonical {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		known = append(known, name)
	}
	return known, v.Absorbed
}

// sourceTagSample renders the claims the tags come from: topic, statement and a short body, capped.
func sourceTagSample(atoms []entities.Atom) string {
	var b strings.Builder
	for _, a := range atoms {
		if b.Len() >= maxSourceTagSample {
			break
		}
		topic := a.Topic
		if topic == "" {
			topic = "uncategorised"
		}
		line := fmt.Sprintf("- [%s] %s", topic, oneLine(a.Claim, 240))
		if body := oneLine(a.Body, 240); body != "" && body != oneLine(a.Claim, 240) {
			line += " — " + body
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// normalizeSourceAutoTags cleans a model answer: a valid source tag, deduplicated, mapped through what
// the vocabulary already merged away, and bounded.
func normalizeSourceAutoTags(raw []string, max int, absorbed map[string]string) []string {
	if max <= 0 {
		max = 6
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, r := range raw {
		tag := coremarkdown.NormalizeTag(r)
		if tag == "" || seen[tag] {
			continue
		}
		if to, ok := absorbed[tag]; ok {
			tag = to
			if seen[tag] {
				continue
			}
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) >= max {
			break
		}
	}
	return out
}

func (uc TagSource) fail(ctx context.Context, run entities.Run, msg string) error {
	run.Status = entities.RunFailed
	run.Error = msg
	run.EndedAt = uc.Clock.Now()
	if uc.Runs != nil {
		if fErr := uc.Runs.FinishRun(ctx, run); fErr != nil && uc.Log != nil {
			uc.Log.Warn("source tags: the failed run could not be recorded", "hash", run.TargetID, "run", run.ID.String(), "err", fErr)
		}
	}
	uc.log(ctx, run.TargetID, run.ID, "error", "auto tags failed", map[string]any{"err": msg})
	if uc.Log != nil {
		uc.Log.Warn("source auto tags failed", "hash", run.TargetID, "run", run.ID.String(), "err", msg)
	}
	return errors.New(msg)
}

func (uc TagSource) log(ctx context.Context, hash string, runID ulid.ULID, level, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), RunID: runID.String(), Phase: "tags", Level: level, Message: msg, Fields: fields,
	})
}
