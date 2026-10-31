package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// TagResult reports what the page carries after the run.
type TagResult struct {
	Page   ulid.ULID
	Tags   []string
	Tokens int
}

// maxTagSample bounds how much of the page the model sees. A wiki page is often a long link list;
// the head of it (plus the title) is what the tags come from.
const maxTagSample = 6000

// TagPage derives the tags of one wiki page from its own title and prose.
func (uc Vocabulary) TagPage(ctx context.Context, id ulid.ULID) (TagResult, error) {
	res := TagResult{Page: id}
	page, err := uc.Wiki.Read(ctx, id)
	if err != nil {
		return res, err
	}
	if uc.Gateway == nil || uc.Model == "" {
		return res, fmt.Errorf("tagging: no model configured")
	}
	limit := uc.Max
	if limit <= 0 {
		limit = 6
	}

	body := strings.TrimSpace(page.Body)
	if utf8.RuneCountInString(body) > maxTagSample {
		body = string([]rune(body)[:maxTagSample])
	}
	prompt := fmt.Sprintf("Title: %s\n\n%s", strings.TrimSpace(page.Title), body)
	if known := uc.knownTags(ctx); len(known) > 0 {
		// The vocabulary is what keeps a tag from being reinvented: give the model the shelves that
		// already exist and it reuses one instead of coining a near-synonym.
		prompt += "\n\nTags already used in this vault (reuse one when it fits):\n- " + strings.Join(known, "\n- ")
	}

	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
		Model:           uc.Model,
		ReasoningEffort: "minimal",
		Messages: []interfaces.AgentMessage{
			{Role: "system", Content: agents.WikiTagsSystem},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return res, fmt.Errorf("tagging the page: %w", err)
	}
	res.Tokens = resp.TotalTokens

	tags := uc.normalize(parseTags(resp.Message.Content), limit)
	if len(tags) == 0 {
		if uc.Log != nil {
			uc.Log.Warn("tagging: the answer held no usable tag", "page", id.String(),
				"head", logHead(resp.Message.Content))
		}
		return res, nil
	}

	// The page is the truth: re-read it right before writing, so a human tag added while the model was
	// thinking is not overwritten with the copy this run started from.
	page, err = uc.Wiki.Read(ctx, id)
	if err != nil {
		return res, err
	}
	page.AutoTags = tags
	if uc.Clock != nil {
		page.UpdatedAt = uc.Clock.Now()
	}
	if err := uc.Wiki.Write(ctx, page); err != nil {
		return res, err
	}
	res.Tags = tags
	return res, nil
}

// knownTags is the tag vocabulary as a flat list: the canonical names plus everything they absorbed.
func (uc Vocabulary) knownTags(_ context.Context) []string {
	if uc.Vocab == nil {
		return nil
	}
	v, err := uc.Vocab.Read(entities.VocabularyWikiTags)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(v.Canonical))
	for _, name := range v.Canonical {
		if name = strings.TrimSpace(name); name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// normalize cleans the model's answer: a valid tag, deduplicated, mapped through what the vocabulary
// already merged away, and bounded.
func (uc Vocabulary) normalize(raw []string, limit int) []string {
	absorbed := uc.absorbed(context.Background())
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
		if len(out) >= limit {
			break
		}
	}
	return out
}

// absorbed is the tag vocabulary's alias table: the names a curation merged away.
func (uc Vocabulary) absorbed(_ context.Context) map[string]string {
	if uc.Vocab == nil {
		return nil
	}
	v, err := uc.Vocab.Read(entities.VocabularyWikiTags)
	if err != nil {
		return nil
	}
	return v.Absorbed
}

// parseTags reads the model's JSON answer. It tolerates prose and code fences, because a model that
// explains itself is not wrong — the tags it named are the answer.
func parseTags(raw string) []string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		var in struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal([]byte(raw[start:end+1]), &in); err == nil && len(in.Tags) > 0 {
			return in.Tags
		}
	}
	// A model that answered with a bare list ("go, caching, tooling") is still answering.
	var out []string
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*+# "))
		if line == "" || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") {
			continue
		}
		for part := range strings.SplitSeq(line, ",") {
			if p := strings.Trim(strings.TrimSpace(part), `"'`+"`"); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
