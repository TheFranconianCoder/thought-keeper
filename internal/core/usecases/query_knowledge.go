package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// QueryKnowledge answers a question from the claims and source overviews, with citations.
type QueryKnowledge struct {
	Atoms    interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
	Search   interfaces.SearchRepository
	// Index answers the recency window (`list_sources`) and the source-tag fallback; Store reads a
	// source's overview sidecar (`read_source_summary`). Both are optional so a unit test can run
	// without a vault; the tools then report that the source side is not configured.
	Index interfaces.IndexRepository
	Store interfaces.SourceStore
	// Asks persists every successful question/answer in the vault. Nil means no persistence (a
	// unit test or an embedding without a vault).
	Asks    interfaces.AskStore
	Runs    interfaces.RunRepository
	Gateway interfaces.AgentGateway
	Model   string
	// ReasoningEffort is how much the model may deliberate ('' for the endpoint's default).
	ReasoningEffort string
	MaxIterations   int
	Clock           interfaces.Clock
	Log             *slog.Logger
}

// QueryResult summarizes a query run.
type QueryResult struct {
	Answer      string
	Iterations  int
	TotalTokens int
}

// The recency window's bounds. One tool answer is capped at 6 000 characters by the loop, so the
// list is PAGED: eight sources with a short overview excerpt per page, and the agent follows
// next_offset until the window is covered. A window too large for the transcript is grouped and
// named, never silently truncated; the model opens a full overview only when it needs it.
const (
	recentSourceLimit    = 8
	overviewExcerptChars = 400
	summaryReadChars     = 5000
	claimFallbackLimit   = 20
	sourceClaimListLimit = 30
)

// Execute runs the agent and finishes the run row.
func (uc QueryKnowledge) Execute(ctx context.Context, runID ulid.ULID, question string) (QueryResult, error) {
	tools := []agents.Tool{
		{
			Def: interfaces.AgentToolDef{
				Name:        "list_topics",
				Description: "List the claim topics with their claim counts.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				topics, err := uc.Atoms.ListClaimTopics(ctx)
				if err != nil {
					return "", err
				}
				m := make([]map[string]any, 0, len(topics))
				for _, t := range topics {
					m = append(m, map[string]any{"topic": t.Topic, "claims": t.Count})
				}
				out, _ := json.Marshal(m)
				return string(out), nil
			},
		},
		{
			Def: interfaces.AgentToolDef{
				Name: "search",
				Description: "Search the CLAIMS. START HERE: one query with the question's key words. " +
					"The search is language-aware: it expands inflections and word families, and uses the " +
					"languages present in the vault. If the question's language differs from the vault, " +
					"also try the key terms in the vault's language. The top hits come back with their full " +
					"claim and body, so the answer can often be written from this call alone. Short " +
					"separate terms work best; symbols such as C++, C# and .NET are supported. If a search " +
					"returns nothing, retry with one or two distinctive terms instead of repeating the same " +
					"phrase.",
				Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			},
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Query string `json:"query"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				hits, err := uc.Search.Search(ctx, in.Query, "atoms", 20)
				if err != nil {
					return "", err
				}
				m := make([]map[string]any, 0, len(hits))
				for _, h := range hits {
					hit := map[string]any{"id": h.ID, "kind": "claim", "snippet": h.Snippet}
					if id, err := ulid.ParseStrict(h.ID); err == nil {
						if a, err := uc.Atoms.GetAtom(ctx, id); err == nil {
							hit["claim"] = a.Claim
							hit["body"] = a.Body
							hit["topic"] = a.Topic
							hit["type"] = string(a.Type)
							hit["status"] = string(a.Status)
						}
					}
					m = append(m, hit)
				}
				out, _ := json.Marshal(m)
				return string(out), nil
			},
		},
		{
			Def: interfaces.AgentToolDef{
				Name:        "read_atom",
				Description: "Read one claim by id (claim, body, topic, type, status) together with its cluster peers: the claims it travels with.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
			},
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				id, err := parseID(args)
				if err != nil {
					return "", err
				}
				a, err := uc.Atoms.GetAtom(ctx, id)
				if err != nil {
					return "", err
				}
				// The claim's cluster is CONTEXT for the answer: the claims it travels with are the
				// nearest evidence that the subject has more to it than this one sentence. Bounded, and
				// ids only for the follow-up — the model reads a peer if it needs the body.
				peerIDs := uc.clusterPeers(ctx, a.ID)
				peers := make([]map[string]string, 0, len(peerIDs))
				for _, pid := range peerIDs {
					if p, gErr := uc.Atoms.GetAtom(ctx, pid); gErr == nil {
						peers = append(peers, map[string]string{
							"id": p.ID.String(), "claim": p.Claim, "topic": p.Topic,
						})
					}
				}
				out, _ := json.Marshal(map[string]any{
					"id": a.ID.String(), "claim": a.Claim, "body": a.Body, "topic": a.Topic,
					"type": string(a.Type), "status": string(a.Status), "cluster_peers": peers,
				})
				return string(out), nil
			},
		},
		{
			Def: interfaces.AgentToolDef{
				Name: "list_atoms",
				Description: "List claims (id, topic, claim), optionally filtered by topic or by the hash " +
					"of the source they came from. Use `source` when a source has no overview yet and you " +
					"need its claims for a digest.",
				Parameters: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string"},"source":{"type":"string"}}}`),
			},
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Topic  string `json:"topic"`
					Source string `json:"source"`
				}
				_ = json.Unmarshal(args, &in)
				var (
					atoms []entities.Atom
					err   error
				)
				if hash := strings.TrimSpace(in.Source); hash != "" {
					if !entities.IsSHA256Hex(hash) {
						return "", fmt.Errorf("invalid source hash %q", hash)
					}
					atoms, err = uc.Atoms.ListAtomsBySource(ctx, hash)
					if len(atoms) > sourceClaimListLimit {
						atoms = atoms[:sourceClaimListLimit]
					}
				} else {
					atoms, err = uc.Atoms.ListAtoms(ctx, in.Topic, 50, 0)
				}
				if err != nil {
					return "", err
				}
				m := make([]map[string]string, 0, len(atoms))
				for _, a := range atoms {
					m = append(m, map[string]string{
						"id": a.ID.String(), "topic": a.Topic, "claim": a.Claim,
						"type": string(a.Type), "status": string(a.Status),
					})
				}
				out, _ := json.Marshal(m)
				return string(out), nil
			},
		},
		{
			Def: interfaces.AgentToolDef{
				Name: "list_sources",
				Description: "List the newest SOURCES in the vault, optionally within a time window. " +
					"START HERE for questions about what is new, recent or \"the last 24 hours\" — do not " +
					"search claims first. `since` accepts an RFC3339 timestamp or a duration back from " +
					"now such as 24h, 7d or 168h; omit it for the newest sources without a window. Returns " +
					"a page of sources: hash, title, arrival time, kind, tags, claim count, derivation " +
					"state and a short excerpt of the overview, plus the window's total. If `next_offset` " +
					"is greater than 0, call the tool again with that offset to read the rest of the " +
					"window. Bookmarks and sitemap/feed containers are counted but not listed: they have " +
					"no overview. The window is VAULT ARRIVAL, not the publication date.",
				Parameters: json.RawMessage(`{"type":"object","properties":{"since":{"type":"string"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
			},
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Since  string `json:"since"`
					Limit  int    `json:"limit"`
					Offset int    `json:"offset"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				if uc.Index == nil {
					return "", fmt.Errorf("the source index is not configured")
				}
				since, err := ParseSince(in.Since, uc.Clock.Now())
				if err != nil {
					return "", err
				}
				limit := in.Limit
				if limit <= 0 {
					limit = recentSourceLimit
				}
				if limit > recentSourceLimit {
					limit = recentSourceLimit
				}
				offset := in.Offset
				if offset < 0 {
					offset = 0
				}
				recent, err := uc.Index.ListRecentSources(ctx, since, limit, offset)
				if err != nil {
					return "", err
				}
				sources := make([]map[string]any, 0, len(recent.Sources))
				for _, src := range recent.Sources {
					state := src.Steps.State(entities.StepDistill)
					item := map[string]any{
						"hash":        src.Hash,
						"title":       sourceLabel(src),
						"kind":        string(src.Kind),
						"imported_at": src.ImportedAt.UTC().Format(time.RFC3339),
						"tags":        src.AllTags(),
						"atoms":       src.AtomCount,
						"distill":     string(state),
						"overview":    false,
					}
					// An overview is written after a COMPLETE distill. A stale or failed step can
					// leave an older summary behind; reading it would describe a text that is no
					// longer the source.
					if state == entities.StepOK && uc.Store != nil {
						if rc, oErr := uc.Store.OpenRendered(src.Hash, entities.SidecarSummary); oErr == nil {
							if text, rErr := readSummary(rc); rErr == nil && strings.TrimSpace(text) != "" {
								item["overview"] = true
								item["excerpt"] = Excerpt(text, overviewExcerptChars)
							}
						}
					}
					sources = append(sources, item)
				}
				nextOffset := 0
				if offset+len(recent.Sources) < recent.Total {
					nextOffset = offset + len(recent.Sources)
				}
				res := map[string]any{
					"sources":     sources,
					"count":       len(sources),
					"total":       recent.Total,
					"offset":      offset,
					"next_offset": nextOffset,
					"bookmarks":   recent.Bookmarks,
					"containers":  recent.Containers,
					"now":         uc.Clock.Now().UTC().Format(time.RFC3339),
				}
				if !since.IsZero() {
					res["since"] = since.UTC().Format(time.RFC3339)
				}
				out, mErr := json.Marshal(res)
				if mErr != nil {
					return "", mErr
				}
				return string(out), nil
			},
		},
		{
			Def: interfaces.AgentToolDef{
				Name: "read_source_summary",
				Description: "Read one source's overview in full, by hash. Use it for at most THREE " +
					"sources in a digest when the excerpt from list_sources is not enough. If no overview " +
					"has been written yet, it returns the source's claims instead (or the derivation " +
					"state while the source is still being processed).",
				Parameters: json.RawMessage(`{"type":"object","properties":{"hash":{"type":"string"}},"required":["hash"]}`),
			},
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Hash string `json:"hash"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				if uc.Index == nil {
					return "", fmt.Errorf("the source index is not configured")
				}
				hash := strings.TrimSpace(in.Hash)
				if !entities.IsSHA256Hex(hash) {
					return "", fmt.Errorf("invalid source hash %q", hash)
				}
				src, err := uc.Index.GetSourceByHash(ctx, hash)
				if err != nil {
					return "", err
				}
				state := src.Steps.State(entities.StepDistill)
				res := map[string]any{
					"hash":        src.Hash,
					"title":       sourceLabel(src),
					"imported_at": src.ImportedAt.UTC().Format(time.RFC3339),
					"available":   false,
					"distill":     string(state),
				}
				if state == entities.StepOK && uc.Store != nil {
					if rc, oErr := uc.Store.OpenRendered(hash, entities.SidecarSummary); oErr == nil {
						if text, rErr := readSummary(rc); rErr == nil && strings.TrimSpace(text) != "" {
							res["available"] = true
							res["overview"] = Excerpt(text, summaryReadChars)
						}
					}
				}
				if available, _ := res["available"].(bool); !available {
					if state == entities.StepOK && uc.Atoms != nil {
						atoms, aErr := uc.Atoms.ListAtomsBySource(ctx, hash)
						if aErr == nil && len(atoms) > 0 {
							res["note"] = "No overview has been written yet; these are the source's claims."
							claims := make([]map[string]string, 0, claimFallbackLimit)
							for i, a := range atoms {
								if i >= claimFallbackLimit {
									break
								}
								claims = append(claims, map[string]string{
									"id": a.ID.String(), "topic": a.Topic, "claim": a.Claim,
								})
							}
							res["claims"] = claims
						} else if aErr == nil {
							res["note"] = "The source is derived but has no claims, so there is nothing to summarise."
						}
					} else if state != entities.StepOK {
						res["note"] = "This source is still being derived; its content is not ready to summarise."
					}
				}
				out, mErr := json.Marshal(res)
				if mErr != nil {
					return "", mErr
				}
				return string(out), nil
			},
		},
	}

	var languages []string
	if finder, ok := uc.Search.(interface {
		Languages(context.Context) ([]string, error)
	}); ok {
		if found, err := finder.Languages(ctx); err == nil {
			languages = found
		}
	}

	rec := NewStepRecorder(ctx, uc.Runs, runID, uc.Log)
	loop := agents.Loop{
		Gateway:         uc.Gateway,
		Model:           uc.Model,
		ReasoningEffort: uc.ReasoningEffort,
		System:          agents.QuerySystem,
		Tools:           tools,
		MaxIterations:   uc.MaxIterations,
		OnStep:          rec.OnStep,
		OnTurn:          turnLogger(uc.Log, "query"),
	}
	res, runErr := loop.Run(ctx, agents.QueryUser(question, languages, uc.Clock.Now()))

	run := entities.Run{
		ID: runID, Op: entities.RunQuery, Model: uc.Model,
		PromptTokens: res.Usage.PromptTokens, CompletionTokens: res.Usage.CompletionTokens,
		TotalTokens: res.Usage.TotalTokens, Iterations: res.Iterations, EndedAt: uc.Clock.Now(),
	}
	if runErr != nil {
		run.Status = entities.RunFailed
		run.Error = runErr.Error()
		_ = uc.Runs.FinishRun(ctx, run)
		return QueryResult{Iterations: res.Iterations, TotalTokens: res.Usage.TotalTokens}, runErr
	}
	if res.Truncated && uc.Log != nil {
		// The tool budget was spent and the answer came from what was read. Worth saying: the answer
		// may be partial, and the next question can be narrower.
		uc.Log.Warn("query answered from what was read: the tool budget was spent", "iterations", res.Iterations)
	}
	run.Status = entities.RunSucceeded
	// The saved file must already contain ordinary Markdown links; the [[claim:...]] and
	// [[source:...]] syntax is only the model's intermediate citation form.
	answer := coremarkdown.NormalizeAskAnswer(res.Content)
	run.Answer = answer
	_ = uc.Runs.FinishRun(ctx, run)
	// EVERY successful Ask is saved; there is no separate "file" action any more. The run id is the
	// record's id, so the saved answer stays traceable to the run that produced it.
	if uc.Asks != nil {
		now := uc.Clock.Now()
		if err := uc.Asks.Write(ctx, entities.Ask{
			ID: runID, Question: question, Answer: answer, Tags: AskTags(ctx, question, answer, uc.Atoms, uc.Index),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil && uc.Log != nil {
			uc.Log.Warn("ask: could not save the question/answer", "run", runID, "err", err)
		}
	}
	return QueryResult{Answer: answer, Iterations: res.Iterations, TotalTokens: res.Usage.TotalTokens}, nil
}

// searchExcerpts is how many of a search's hits carry their text, and how much of it: enough to
// answer from, small enough that one tool result stays a tool result (the transcript is capped too).
const (
	searchExcerpts     = 3
	searchExcerptChars = 1200
)

// clusterPeers returns the other claims of the claim's clusters, bounded: the answer may use them as
// context, and the ids let the model read one in full if it needs the body.
func (uc QueryKnowledge) clusterPeers(ctx context.Context, id ulid.ULID) []ulid.ULID {
	if uc.Clusters == nil {
		return nil
	}
	clusters, err := uc.Clusters.ClusterIDsOfAtom(ctx, id)
	if err != nil {
		return nil
	}
	seen := map[ulid.ULID]bool{id: true}
	out := make([]ulid.ULID, 0, 8)
	for _, clusterID := range clusters {
		members, mErr := uc.Clusters.AtomsOfCluster(ctx, clusterID)
		if mErr != nil {
			continue
		}
		for _, member := range members {
			if seen[member] {
				continue
			}
			seen[member] = true
			out = append(out, member)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}

// sourceLabel is the name a reader sees for a source: the document's title, then the name it came
// in under, then a short hash. It is the same fallback the Sources lens uses; the model needs a
// name to link and never the raw address alone.
func sourceLabel(src entities.Source) string {
	if title := strings.TrimSpace(src.Title); title != "" {
		return title
	}
	if name := strings.TrimSpace(src.OriginalName); name != "" {
		return name
	}
	if len(src.Hash) > 12 {
		return src.Hash[:12]
	}
	return src.Hash
}

// readSummary reads an overview sidecar and closes it. The cap is a safety net against a corrupted
// file; a summary is prose, not a stream the answer keeps.
func readSummary(rc io.ReadCloser) (text string, err error) {
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	body, rErr := io.ReadAll(io.LimitReader(rc, 64<<10))
	return string(body), rErr
}

// Excerpt cuts a page's text to n characters at a word boundary, and says that it was cut.
func Excerpt(text string, n int) string {
	text = strings.TrimSpace(text)
	if len(text) <= n {
		return text
	}
	cut := text[:n]
	if i := strings.LastIndexAny(cut, " \n"); i > n/2 {
		cut = cut[:i]
	}
	return cut + " …"
}

func parseID(args json.RawMessage) (ulid.ULID, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ulid.ULID{}, err
	}
	id, err := ulid.ParseStrict(strings.TrimSpace(in.ID))
	if err != nil {
		return ulid.ULID{}, fmt.Errorf("invalid id %q", in.ID)
	}
	return id, nil
}

func idsOf(ids []ulid.ULID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
