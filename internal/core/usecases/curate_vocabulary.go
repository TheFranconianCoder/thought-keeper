package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// CurateVocabulary asks the model what the claim-topic vocabulary should become.
//
// A topic is the flat subject of a CLAIM. It is a free name invented at write time, so it drifts. The
// pass merges names that mean the same thing, and nothing else: a rename changes only the claims'
// label. No prose is derived again, and there is no page vocabulary any more.
type CurateVocabulary struct {
	Namespace entities.VocabularyNamespace
	Report    VocabularyReport
	Atoms     interfaces.AtomRepository
	Gateway   interfaces.AgentGateway
	Model     string
	Apply     bool
	Vocab     interfaces.VocabularyStore
	// Wiki applies a TAG rename (the wiki-tags namespace) to the pages' frontmatter.
	Wiki interfaces.WikiStore
	// Sources applies a TAG rename (the source-tags namespace) to the sources' records.
	Sources   interfaces.SourceTagStore
	AtomStore interfaces.AtomStore
	Runs      interfaces.RunRepository
	Clock     interfaces.Clock
	Log       *slog.Logger
	OnPhase   func(phase string)
	Parallel  int
	ApplyGate func(ctx context.Context, fn func(ctx context.Context) error) error
}

// CurateResult is what the model answered.
type CurateResult struct {
	Namespace string
	Canonical []string
	Mapping   map[string]string
	Names     int
	Tokens    int
	Applied   ApplyVocabularyResult
	Residual  []string
}

const (
	maxNamesInPrompt = 1000
	maxChunks        = 64
	samplesPerName   = 0
)

func (uc CurateVocabulary) phase(name string) {
	if uc.OnPhase != nil {
		uc.OnPhase(name)
	}
}

const vocabularyCallTimeout = 10 * time.Minute

// Execute asks the model about one namespace and applies the answer.
func (uc CurateVocabulary) Execute(ctx context.Context, runID ulid.ULID) (res CurateResult, err error) {
	res = CurateResult{Namespace: string(uc.Namespace), Mapping: map[string]string{}}
	finished := false
	finish := func(status entities.RunStatus, answer string) {
		finished = true
		if uc.Runs == nil {
			return
		}
		run := entities.Run{
			ID: runID, Op: entities.RunVocabulary, Model: uc.Model, Status: status,
			Answer: answer, TotalTokens: res.Tokens, EndedAt: uc.Clock.Now(),
		}
		if status == entities.RunFailed {
			run.Error = answer
			run.Answer = ""
		}
		_ = uc.Runs.FinishRun(ctx, run)
	}
	defer func() {
		if finished {
			return
		}
		if err != nil {
			finish(entities.RunFailed, err.Error())
			return
		}
		touched, unit := res.Applied.Atoms, "claims"
		switch uc.Namespace {
		case entities.VocabularyWikiTags:
			touched, unit = res.Applied.Pages, "pages"
		case entities.VocabularySourceTags:
			touched, unit = res.Applied.Sources, "sources"
		}
		finish(entities.RunSucceeded, fmt.Sprintf("%s: %d names in, %d canonical, %d merged, %d %s relabelled",
			res.Namespace, res.Names, len(res.Canonical), len(res.Mapping), touched, unit))
	}()
	if uc.Gateway == nil || uc.Model == "" {
		return res, fmt.Errorf("vocabulary: no model configured")
	}
	// The catalog of THIS namespace. Broad tokens and near-duplicate families are namespace-local:
	// a source tag must be judged with the source tags, not with the claim topics.
	healths, err := uc.Report.Execute(ctx)
	if err != nil {
		return res, err
	}
	var observed []nameCount
	var observedUses []partialentities.NameUses
	for _, x := range healths {
		if x.Namespace != string(uc.Namespace) {
			continue
		}
		observedUses = x.Observed
		for _, t := range x.Observed {
			observed = append(observed, nameCount{name: t.Name, uses: t.Uses})
		}
		break
	}
	var vocab entities.Vocabulary
	if uc.Vocab != nil {
		if v, vErr := uc.Vocab.Read(uc.Namespace); vErr == nil {
			vocab = v
		}
	}
	if len(vocab.BroadTokens) == 0 {
		// Learn the broad tokens from THIS vault before deciding which names are near-duplicates.
		// The first pass also re-opens the already judged names: the old detector marked a giant
		// chain of unrelated names as decided, and without this reset the improved grouping would
		// never be applied to them.
		broad := uc.learnBroadTokens(ctx, observedUses)
		vocab.BroadTokens = broad
		if len(vocab.Decided) > 0 {
			vocab.Decided = nil
		}
		if uc.Vocab != nil {
			if uc.Clock != nil {
				vocab.Updated = uc.Clock.Now()
			}
			if wErr := uc.Vocab.Write(vocab); wErr != nil && uc.Log != nil {
				uc.Log.Warn("vocabulary: broad tokens not saved", "err", wErr)
			}
		}
		// The families are built WITH the broad tokens; re-read the health so the freshly learned
		// set orders the candidates instead of the fallback.
		healths, err = uc.Report.Execute(ctx)
		if err != nil {
			return res, err
		}
	}
	flagged := map[string]bool{}
	for _, x := range healths {
		if x.Namespace != string(uc.Namespace) {
			continue
		}
		for _, g := range append(append([]partialentities.NameGroup{}, x.Collisions...), x.NearDuplicates...) {
			for _, n := range g.Names {
				flagged[n.Name] = true
			}
		}
		break
	}
	// The curation still sees the whole unjudged catalog, so a new or flagged name can be merged
	// into an existing name it does not share a token with. The flagged map only orders the
	// question; the giant families are no longer candidates at all.
	chosen := familyOrderedNameUses(observed, flagged, vocab.Judged)
	if len(chosen) == 0 {
		return res, nil
	}
	chunks := chunkByFamily(chosen, maxNamesInPrompt, maxChunks)
	res.Names = len(chosen)
	if uc.Log != nil {
		uc.Log.Info("vocabulary: catalog in batches", "namespace", string(uc.Namespace),
			"names", len(chosen), "questions", len(chunks), "per_question", maxNamesInPrompt, "flagged", len(flagged))
	}
	answers := uc.askAll(ctx, chunks)
	var firstErr error
	for i := range answers {
		if answers[i].err != nil {
			if firstErr == nil {
				firstErr = answers[i].err
			}
			continue
		}
		if err := uc.applyChunk(ctx, answers[i], i+1, len(answers), &res); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return res, firstErr
}

// chunkAnswer is one question and what the model answered.
type chunkAnswer struct {
	chunk     []nameCount
	mapping   map[string]string
	canonical []string
	tokens    int
	err       error
}

func (uc CurateVocabulary) askAll(ctx context.Context, chunks [][]nameCount) []chunkAnswer {
	answers := make([]chunkAnswer, len(chunks))
	if len(chunks) == 0 {
		return answers
	}
	width := uc.Parallel
	if width <= 0 {
		width = 4
	}
	if width > len(chunks) {
		width = len(chunks)
	}
	uc.phase(fmt.Sprintf("asking the model (%d question(s), %d at a time)", len(chunks), width))
	slots := make(chan struct{}, width)
	var wg sync.WaitGroup
	var done atomic.Int64
	for i := range chunks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			answers[i] = uc.askChunk(ctx, chunks[i])
			if uc.OnPhase != nil {
				uc.phase(fmt.Sprintf("asking the model (%d/%d answered)", done.Add(1), len(chunks)))
			}
		}(i)
	}
	wg.Wait()
	return answers
}

func (uc CurateVocabulary) askChunk(ctx context.Context, chunk []nameCount) chunkAnswer {
	answer := chunkAnswer{chunk: chunk, mapping: map[string]string{}}
	if uc.Gateway == nil || uc.Model == "" {
		answer.err = fmt.Errorf("vocabulary: no model configured")
		return answer
	}
	entries, err := uc.evidence(ctx, chunk)
	if err != nil {
		answer.err = err
		return answer
	}
	callCtx, cancel := context.WithTimeout(ctx, vocabularyCallTimeout)
	defer cancel()
	system := agents.VocabularyCurateSystem + agents.VocabularyNameRules
	target := fmt.Sprintf("These are the flat subjects of CLAIMS (%d names). Merge only names that mean the same subject; a topic is flat (no slash, no hierarchy), and the level of detail stays.", len(chunk))
	if uc.Namespace == entities.VocabularyWikiTags || uc.Namespace == entities.VocabularySourceTags {
		// A tag is the shelf ABOVE a topic, the word a reader filters a list by. The "finest level"
		// rule would push it towards claim subjects, which is the level it exists above.
		system = agents.VocabularyCurateSystem
		what := "WIKI PAGES"
		if uc.Namespace == entities.VocabularySourceTags {
			what = "SOURCES"
		}
		target = fmt.Sprintf("These are the TAGS of %s (%d names): coarse labels a reader files and filters that list by. Merge only names that mean the same label, and keep them coarse.", what, len(chunk))
	}
	user := agents.VocabularyCurateUser(string(uc.Namespace), target, nil, entries)
	ask := func(extra string) (interfaces.AgentResponse, error) {
		messages := []interfaces.AgentMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		}
		if extra != "" {
			messages = append(messages, interfaces.AgentMessage{Role: "user", Content: extra})
		}
		return uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
			Model: uc.Model, ReasoningEffort: "minimal", Messages: messages,
		})
	}
	resp, err := ask("")
	if err != nil {
		answer.err = err
		return answer
	}
	answer.tokens += resp.TotalTokens
	canonical, mapping, parseErr := parseVocabularyAnswer(resp.Message.Content)
	if parseErr != nil {
		if uc.Log != nil {
			uc.Log.Warn("vocabulary: unreadable answer", "names", len(chunk),
				"chars", len(resp.Message.Content), "head", logHead(resp.Message.Content), "err", parseErr)
		}
		// A model that explained itself instead of answering gets one strict retry. The batch is
		// deliberately small (maxNamesInPrompt), so this is a formatting miss, not a context limit.
		resp, err = ask("Your previous answer was not a valid JSON object. Return ONLY the JSON object now; no prose, no code fence.")
		if err != nil {
			answer.err = err
			return answer
		}
		answer.tokens += resp.TotalTokens
		canonical, mapping, parseErr = parseVocabularyAnswer(resp.Message.Content)
		if parseErr != nil {
			answer.err = fmt.Errorf("%w (strict retry also failed)", parseErr)
			return answer
		}
	}
	answer.canonical, answer.mapping = canonical, mapping
	return answer
}

func logHead(s string) string {
	const max = 600
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (uc CurateVocabulary) applyChunk(ctx context.Context, answer chunkAnswer, part, parts int, res *CurateResult) error {
	res.Tokens += answer.tokens
	if uc.Vocab != nil {
		v, vErr := uc.Vocab.Read(uc.Namespace)
		if vErr == nil {
			for _, n := range answer.chunk {
				if !v.Judged(n.name) {
					v.Decided = append(v.Decided, n.name)
				}
			}
			_ = uc.Vocab.Write(v)
		}
	}
	if !uc.Apply || (len(answer.mapping) == 0 && len(answer.canonical) == 0) {
		return nil
	}
	uc.phase(fmt.Sprintf("applying the mapping (%d/%d)", part, parts))
	var applied ApplyVocabularyResult
	apply := func(ctx context.Context) error {
		got, aErr := ApplyVocabularyMapping{
			Namespace: uc.Namespace, Mapping: answer.mapping, Canonical: answer.canonical,
			Vocab: uc.Vocab, Atoms: uc.Atoms, AtomStore: uc.AtomStore, Wiki: uc.Wiki,
			Sources: uc.Sources, Clock: uc.Clock, Log: uc.Log,
		}.Execute(ctx)
		if aErr != nil {
			return fmt.Errorf("applying the mapping: %w", aErr)
		}
		applied = got
		res.Applied.Atoms += got.Atoms
		res.Applied.Pages += got.Pages
		res.Applied.Sources += got.Sources
		return nil
	}
	var err error
	if uc.ApplyGate != nil {
		err = uc.ApplyGate(ctx, apply)
	} else {
		err = apply(ctx)
	}
	if err != nil {
		return err
	}
	res.Canonical = append(res.Canonical, answer.canonical...)
	maps.Copy(res.Mapping, applied.Absorbed)
	return nil
}

type nameCount struct {
	name string
	uses int
	fam  string
}

func firstWord(name string) string {
	for i, r := range name {
		if r == '-' || r == '_' || r == '/' || r == '.' || r == ' ' {
			return name[:i]
		}
	}
	return name
}

// familyOrdered groups candidates by the word they start with, so a question never splits a family.
func familyOrderedNameUses(observed []nameCount, flagged map[string]bool, judged func(string) bool) []nameCount {
	type family struct {
		names   []nameCount
		uses    int
		finding bool
	}
	families := map[string]*family{}
	var order []string
	for _, t := range observed {
		if judged != nil && judged(t.name) {
			continue
		}
		key := firstWord(t.name)
		f, ok := families[key]
		if !ok {
			f = &family{}
			families[key] = f
			order = append(order, key)
		}
		f.names = append(f.names, nameCount{name: t.name, uses: t.uses, fam: key})
		f.uses += t.uses
		if flagged[t.name] {
			f.finding = true
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := families[order[i]], families[order[j]]
		if a.finding != b.finding {
			return a.finding
		}
		if a.uses != b.uses {
			return a.uses > b.uses
		}
		return order[i] < order[j]
	})
	out := make([]nameCount, 0, len(observed))
	for _, key := range order {
		out = append(out, families[key].names...)
	}
	return out
}

func chunkByFamily(chosen []nameCount, max, maxChunks int) [][]nameCount {
	var out [][]nameCount
	var cur []nameCount
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
	}
	for i := 0; i < len(chosen); {
		j := i
		for j < len(chosen) && chosen[j].fam == chosen[i].fam {
			j++
		}
		// Keep one family together where possible, but never let a family larger than the
		// per-question limit become one giant prompt. A chunk is what the model can answer.
		for i < j {
			take := min(j-i, max)
			if len(cur) > 0 && len(cur)+take > max {
				flush()
			}
			cur = append(cur, chosen[i:i+take]...)
			i += take
		}
	}
	flush()
	if len(out) > maxChunks {
		out = out[:maxChunks]
	}
	return out
}

func (uc CurateVocabulary) evidence(ctx context.Context, names []nameCount) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("- %q (%s)", n.name, uses(n.uses)))
	}
	return out, nil
}

func uses(n int) string {
	if n == 1 {
		return "1 use"
	}
	return fmt.Sprintf("%d uses", n)
}

// parseVocabularyAnswer reads the model's JSON answer. It tolerates prose and code fences, because a
// model that explains itself is not wrong — the object it returned is the decision.
func parseVocabularyAnswer(raw string) ([]string, map[string]string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, nil, fmt.Errorf("vocabulary: the answer holds no JSON object")
	}
	var in struct {
		Canonical []string          `json:"canonical"`
		Mapping   map[string]string `json:"mapping"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &in); err != nil {
		return nil, nil, fmt.Errorf("vocabulary: unreadable answer: %w", err)
	}
	if in.Mapping == nil {
		in.Mapping = map[string]string{}
	}
	return in.Canonical, in.Mapping, nil
}

// learnBroadTokens asks the model which tokens are too generic for THIS vault's near-duplicate
// detection. The candidates come from the observed catalog's own document frequency; the model
// chooses among them. With no model, the same frequency signal is used directly.
func (uc CurateVocabulary) learnBroadTokens(ctx context.Context, observed []partialentities.NameUses) []string {
	candidates := tokenCandidates(observed)
	if len(candidates) == 0 {
		return nil
	}
	if uc.Gateway != nil && uc.Model != "" {
		var listing strings.Builder
		for _, c := range candidates {
			fmt.Fprintf(&listing, "- %s — %d names\n", c.token, c.uses)
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		resp, err := uc.Gateway.Complete(callCtx, interfaces.AgentRequest{
			Model:           uc.Model,
			ReasoningEffort: "minimal",
			Messages: []interfaces.AgentMessage{
				{Role: "system", Content: agents.BroadTokensSystem},
				{Role: "user", Content: agents.BroadTokensUser(listing.String())},
			},
		})
		if err == nil {
			if tokens := parseBroadTokens(resp.Message.Content, candidates); len(tokens) > 0 {
				return tokens
			}
		} else if uc.Log != nil {
			uc.Log.Warn("vocabulary: broad tokens not learned by the model", "err", err)
		}
	}
	// Deterministic fallback: tokens the catalog itself uses across many names. This is still
	// learned from the vault, not a hardcoded language list.
	fallback := fallbackBroadTokens(observed)
	tokens := make([]string, 0, len(fallback))
	for token := range fallback {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

type tokenUse struct {
	token string
	uses  int
}

func tokenCandidates(observed []partialentities.NameUses) []tokenUse {
	df := map[string]int{}
	for _, o := range observed {
		for token := range distinctiveTokens(o.Name, nil) {
			df[token]++
		}
	}
	out := make([]tokenUse, 0, len(df))
	for token, n := range df {
		if n >= 4 {
			out = append(out, tokenUse{token: token, uses: n})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].uses != out[j].uses {
			return out[i].uses > out[j].uses
		}
		return out[i].token < out[j].token
	})
	if len(out) > maxAutoBroadTokens {
		out = out[:maxAutoBroadTokens]
	}
	return out
}

func parseBroadTokens(raw string, candidates []tokenUse) []string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil
	}
	var in struct {
		BroadTokens []string `json:"broad_tokens"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &in); err != nil {
		return nil
	}
	allowed := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		allowed[c.token] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in.BroadTokens))
	for _, token := range in.BroadTokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if !allowed[token] || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}
