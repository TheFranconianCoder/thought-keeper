package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// QueryKnowledge answers a question from the claims, with citations.
type QueryKnowledge struct {
	Atoms    interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
	Search   interfaces.SearchRepository
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
				Name:        "list_atoms",
				Description: "List claims (id, topic, claim), optionally filtered by topic.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string"}}}`),
			},
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Topic string `json:"topic"`
				}
				_ = json.Unmarshal(args, &in)
				atoms, err := uc.Atoms.ListAtoms(ctx, in.Topic, 50, 0)
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
	res, runErr := loop.Run(ctx, agents.QueryUser(question, languages))

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
	// The saved file must already contain ordinary Markdown links; the [[claim:...]] syntax is only the
	// model's intermediate citation form.
	answer := coremarkdown.NormalizeAskAnswer(res.Content)
	run.Answer = answer
	_ = uc.Runs.FinishRun(ctx, run)
	// EVERY successful Ask is saved; there is no separate "file" action any more. The run id is the
	// record's id, so the saved answer stays traceable to the run that produced it.
	if uc.Asks != nil {
		now := uc.Clock.Now()
		if err := uc.Asks.Write(ctx, entities.Ask{
			ID: runID, Question: question, Answer: answer, Tags: AskTags(ctx, question, answer, uc.Atoms),
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
