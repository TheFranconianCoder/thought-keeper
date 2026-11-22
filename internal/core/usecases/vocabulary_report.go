package usecases

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// Vocabulary is the use case of the three name vocabularies: the report that says how healthy they
// are, the reconcile that repairs them, and the curation that asks the model to merge names.
type Vocabulary struct {
	Atoms     interfaces.AtomRepository
	AtomStore interfaces.AtomStore
	Wiki      interfaces.WikiStore
	Reader    interfaces.SourceTagReader
	Sources   interfaces.SourceTagStore
	Tags      interfaces.SourceTagStore
	Index     interfaces.IndexRepository
	Vocab     interfaces.VocabularyStore
	Runs      interfaces.RunRepository
	SourceLog interfaces.SourceLog
	Gateway   interfaces.AgentGateway
	Model     string
	Clock     interfaces.Clock
	Log       *slog.Logger
	// The curation and the naming are modes, set on a copy of the value before the call.
	Namespace entities.VocabularyNamespace
	Mapping   map[string]string
	Canonical []string
	RunID     ulid.ULID
	Apply     bool
	TopNames  int
	Max       int
	// Judged are the names this pass put to the model and did not merge: it decided about them, and
	// the shelf cannot cover them. ApplyMapping records them in Decided in the same write as the
	// mapping, so a declined name is never a question again.
	Judged []string
	// Budget stops a coarsening from LAUNCHING new questions once it is spent; the answers already
	// in flight are applied, so the pass ends on a batch boundary and the gate is released. Zero
	// means the run's own ceiling is the only bound.
	Budget time.Duration
	// Target is the size the COARSENING builds the shelf for (vocabulary.<namespace>.max_names).
	Target int
	// Shelf holds the share limits a topic is judged by (vocabulary.shelf.max_share / min_share).
	Shelf ShelfLimits
	// Parallel bounds the model calls one run keeps in flight: the rebranch's claims, and the
	// coarsening's shelf questions (whose APPLY stays in order regardless). 0 = the use case's own
	// default.
	Parallel  int
	OnPhase   func(phase string)
	ApplyGate func(ctx context.Context, fn func(ctx context.Context) error) error
}

// ShelfLimits are the relative shares a topic is judged by. A share is a topic's claims over ALL
// claims, so the limits hold for any corpus size. A zero limit is off: MaxShare 0 never calls a topic a
// catch-all, MinShare 0 never calls one thin.
type ShelfLimits struct {
	MaxShare float64
	MinShare float64
}

// CatchAll reports whether a topic carries more than the catch-all share of the claims.
func (l ShelfLimits) CatchAll(uses, total int) bool {
	return l.MaxShare > 0 && total > 0 && float64(uses) > l.MaxShare*float64(total)
}

// Thin reports whether a topic carries less than the thin share of the claims (zero uses included).
func (l ShelfLimits) Thin(uses, total int) bool {
	return l.MinShare > 0 && total > 0 && float64(uses) < l.MinShare*float64(total)
}

// VocabularyReporter reports how healthy the vocabularies are. A handler holds this value; nil means
// the feature is not wired.
type VocabularyReporter func(ctx context.Context) ([]partialentities.VocabularyHealth, error)

// Report aggregates the names the vault uses and what they collide with. A namespace with nothing
// observed is left out: an empty catalog is not a health report, and a tag namespace nobody uses yet
// must not read as "0 names, 0% covered".
func (uc Vocabulary) Report(ctx context.Context) ([]partialentities.VocabularyHealth, error) {
	topics, err := uc.observed(ctx)
	if err != nil {
		return nil, err
	}
	out := []partialentities.VocabularyHealth{uc.health(topics, uc.vocabulary(entities.VocabularyTopics), entities.VocabularyTopics)}
	tags, err := uc.observedTags(ctx)
	if err != nil {
		return nil, err
	}
	if len(tags) > 0 {
		out = append(out, uc.health(tags, uc.vocabulary(entities.VocabularyWikiTags), entities.VocabularyWikiTags))
	}
	sourceTags, err := uc.observedSourceTags(ctx)
	if err != nil {
		return nil, err
	}
	if len(sourceTags) > 0 {
		out = append(out, uc.health(sourceTags, uc.vocabulary(entities.VocabularySourceTags), entities.VocabularySourceTags))
	}
	return out, nil
}

// vocabulary reads one namespace's decided names; a missing store (or file) is an empty vocabulary.
func (uc Vocabulary) vocabulary(ns entities.VocabularyNamespace) entities.Vocabulary {
	if uc.Vocab == nil {
		return entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}
	}
	v, err := uc.Vocab.Read(ns)
	if err != nil {
		return entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}
	}
	return v
}

// observedTags counts the tags of every page: the human's and the derived ones.
func (uc Vocabulary) observedTags(ctx context.Context) ([]partialentities.NameUses, error) {
	if uc.Wiki == nil {
		return nil, nil
	}
	pages, err := uc.Wiki.List(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, p := range pages {
		for _, tag := range p.AllTags() {
			counts[tag]++
		}
	}
	out := make([]partialentities.NameUses, 0, len(counts))
	for name, n := range counts {
		out = append(out, partialentities.NameUses{Name: name, Uses: n})
	}
	return out, nil
}

// observedSourceTags counts the human labels of every source. The index mirrors the .meta records, so
// the report does not open 465 files to answer a health question.
func (uc Vocabulary) observedSourceTags(ctx context.Context) ([]partialentities.NameUses, error) {
	if uc.Reader == nil {
		return nil, nil
	}
	counts, err := uc.Reader.SourceTagCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.NameUses, 0, len(counts))
	for name, n := range counts {
		out = append(out, partialentities.NameUses{Name: name, Uses: n})
	}
	return out, nil
}

// observed reads the catalog the index knows: the names that exist right now, with their counts.
//
// The ORDER is part of the contract: most used first, name as the tiebreak. The index answers in
// alphabetical order (ListClaimTopics is ORDER BY topic), and every caller that takes a HEAD of this
// list — the shelf build above all — would otherwise take the alphabetically first names. That is
// exactly how a measured vault got a shelf of "10000-hour-rule … animal-feed-composition" instead of
// its most used names: the build sliced an unsorted catalog, and `health` hid it by sorting its own
// copy afterwards.
func (uc Vocabulary) observed(ctx context.Context) ([]partialentities.NameUses, error) {
	topics, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.NameUses, 0, len(topics))
	for _, t := range topics {
		out = append(out, partialentities.NameUses{Name: t.Topic, Uses: t.Count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Uses != out[j].Uses {
			return out[i].Uses > out[j].Uses
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// keyOf is the deterministic normalization of a name: what two spellings of one name collapse to. A
// topic is a slug path, a tag a short slug — the two normalize differently, and a report that mixed
// them would call "Go/Concurrency" and "go" a collision.
func keyOf(name string, ns entities.VocabularyNamespace) string {
	if ns == entities.VocabularyWikiTags || ns == entities.VocabularySourceTags {
		return coremarkdown.NormalizeTag(name)
	}
	return coremarkdown.NormalizeTopic(name)
}

// health computes the numbers, and the two findings that need no judgement.
func (uc Vocabulary) health(observed []partialentities.NameUses, vocab entities.Vocabulary, ns entities.VocabularyNamespace) partialentities.VocabularyHealth {
	h := partialentities.VocabularyHealth{
		Namespace: string(ns),
		Canonical: len(vocab.Canonical),
		Absorbed:  len(vocab.Absorbed),
	}
	sort.Slice(observed, func(i, j int) bool {
		if observed[i].Uses != observed[j].Uses {
			return observed[i].Uses > observed[j].Uses
		}
		return observed[i].Name < observed[j].Name
	})
	h.Names = len(observed)
	// The sets are built once: a linear scan of a 50,000-name catalog per observed name is a report
	// that takes seconds and times out on the vault it exists for.
	known := make(map[string]bool, len(vocab.Shelf)+len(vocab.Canonical)+len(vocab.Decided)+len(vocab.Absorbed))
	for _, name := range vocab.Shelf {
		known[name] = true
	}
	for _, name := range vocab.Canonical {
		known[name] = true
	}
	for _, name := range vocab.Decided {
		known[name] = true
	}
	for name := range vocab.Absorbed {
		known[name] = true
	}
	for _, o := range observed {
		h.Uses += o.Uses
		if o.Uses == 1 {
			h.Singletons++
		}
		if !known[o.Name] {
			h.Undecided++
		}
	}
	if h.Names > 0 {
		h.SingletonShare = float64(h.Singletons) / float64(h.Names)
	}
	if ns == entities.VocabularyTopics {
		for _, o := range observed {
			if uc.Shelf.CatchAll(o.Uses, h.Uses) {
				h.CatchAll++
				h.CatchAllUses += o.Uses
			}
			if uc.Shelf.Thin(o.Uses, h.Uses) {
				h.Thin++
				h.ThinUses += o.Uses
			}
		}
	}
	h.Observed = append(h.Observed, observed...)
	top := uc.TopNames
	if top <= 0 {
		top = 20
	}
	if len(observed) > top {
		h.Top = append(h.Top, observed[:top]...)
	} else {
		h.Top = append(h.Top, observed...)
	}
	covered := 0
	for _, t := range h.Top {
		covered += t.Uses
	}
	if h.Uses > 0 {
		h.Coverage = float64(covered) / float64(h.Uses)
	}

	// Names that normalize to the same key: two spellings of one name, which is always a bug.
	byKey := map[string][]partialentities.NameUses{}
	for _, o := range observed {
		k := keyOf(o.Name, ns)
		if k == "" {
			continue
		}
		byKey[k] = append(byKey[k], o)
	}
	for _, group := range byKey {
		if len(group) > 1 {
			h.Collisions = append(h.Collisions, partialentities.NameGroup{Reason: "same-key", Names: group})
		}
	}
	sortGroups(h.Collisions)
	h.CollisionCount = len(h.Collisions)

	// Names that share their words: cache-invalidation and caching-invalidation are candidates. The
	// threshold is deliberately low-ish but never a decision — a candidate is a question. Broad
	// tokens are learned from this vault (never a hardcoded word list) so that "go-http" and
	// "go-language" are not chained through the language name.
	broad := vocab.BroadTokenSet()
	if len(broad) == 0 {
		broad = fallbackBroadTokens(observed)
	}
	judged := make(map[string]bool, len(vocab.Decided))
	for _, name := range vocab.Decided {
		judged[name] = true
	}
	h.Shelf = len(vocab.Shelf)
	h.Target = vocab.Target
	tail := tailStateOf(observed, vocab)
	h.Remaining, h.Retopic, h.OffShelf = tail.remaining, tail.retopic, tail.offShelf
	h.RetopicClaims = tail.retopicClaims
	h.Retopiced = len(vocab.Relabelled)
	if ns != entities.VocabularyTopics {
		// The claim-level queue is a TOPICS concept: a tag is not re-derived from a claim, and "claims
		// off the shelf" would be a count of SOURCE or PAGE tag uses wearing the wrong unit. The names
		// a coarsening still has to file (`Remaining`) stay — that is real work for a tag namespace.
		h.Retopic, h.RetopicClaims, h.Retopiced, h.OffShelf = 0, 0, 0, 0
	}
	h.BroadTokens = len(broad)
	h.BroadTokensLearned = len(vocab.BroadTokens) > 0
	h.NearDuplicates, h.OversizedFamilies, h.OversizedNames = nearDuplicates(observed, broad)
	h.NearDuplicateCount = len(h.NearDuplicates)
	for _, group := range h.NearDuplicates {
		for _, name := range group.Names {
			if !judged[name.Name] {
				h.UnreviewedFamilies++
				break
			}
		}
	}
	return h
}

// tailState splits the names outside the shelf by the question each one is still in. They are three
// different numbers and confusing them made the report describe an endless queue:
//
//   - remaining: what the COARSENING still asks — outside the shelf, not absorbed, not judged. A name
//     the model passed over is judged, so it is not asked again in bulk.
//   - retopic: what the RETOPIC pass still asks — outside the shelf, not absorbed, not yet re-derived
//     claim by claim. It includes the judged names: "the shelf cannot cover this name" is exactly the
//     case where the CLAIMS have to be looked at one by one. retopicClaims is the same queue in CLAIMS:
//     the progress measure of an automatic pass, which can spend a whole budget inside one name.
//   - offShelf: the claim MASS still outside the shelf. It is the honest distance to the invariant
//     ("every claim's topic is on the shelf") and the only number that says whether the corpus moved.
type tailState struct {
	remaining     int
	retopic       int
	retopicClaims int
	offShelf      int
}

// tailStateOf counts the tail once, from sets built for the whole catalog.
func tailStateOf(observed []partialentities.NameUses, vocab entities.Vocabulary) tailState {
	var st tailState
	if len(observed) == 0 {
		return st
	}
	if len(vocab.Shelf) == 0 {
		// No target, no tail to reduce: everything is "remaining" in the sense of the coarsening's
		// queue, but nothing is off a shelf that does not exist and no claim can be re-derived onto it.
		return tailState{remaining: len(observed)}
	}
	onShelf := make(map[string]bool, len(vocab.Shelf))
	for _, name := range vocab.Shelf {
		onShelf[name] = true
	}
	judged := make(map[string]bool, len(vocab.Decided))
	for _, name := range vocab.Decided {
		judged[name] = true
	}
	relabelled := make(map[string]bool, len(vocab.Relabelled))
	for _, name := range vocab.Relabelled {
		relabelled[name] = true
	}
	for _, o := range observed {
		if onShelf[o.Name] {
			continue
		}
		if _, absorbed := vocab.Absorbed[o.Name]; absorbed {
			continue
		}
		st.offShelf += o.Uses
		if relabelled[o.Name] {
			continue
		}
		st.retopic++
		st.retopicClaims += o.Uses
		if !judged[o.Name] {
			st.remaining++
		}
	}
	return st
}

// nearDuplicates groups the names whose token sets overlap strongly. It reports GROUPS, not pairs:
// a name that shares a word with twelve others is ONE candidate family, and a pair count is a number
// that cannot be right (652 topic names, "1241 near-duplicates" — the same name counted once per pair,
// and the model handed the same family twelve times). A group is a question; a pair is a statistic.
func nearDuplicates(observed []partialentities.NameUses, broad map[string]bool) ([]partialentities.NameGroup, int, int) {
	tokens := make([][]string, len(observed))
	all := make([]map[string]bool, len(observed))
	byToken := map[string][]int{}
	for i, o := range observed {
		all[i] = distinctiveTokens(o.Name, broad)
		tokens[i] = sortedTokenList(all[i])
		for _, t := range tokens[i] {
			byToken[t] = append(byToken[t], i)
		}
	}
	parent := make([]int, len(observed))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	seen := map[[2]int]bool{}
	for i := range observed {
		// A single distinctive token is not enough evidence to call two names a near-duplicate:
		// after broad tokens are removed, "any-type" and "evolving-any" would both reduce to one
		// generic word. Two tokens keep the comparison meaningful.
		if len(tokens[i]) < 2 {
			continue
		}
		candidates := map[int]bool{}
		for _, t := range tokens[i] {
			for _, j := range byToken[t] {
				if j > i {
					candidates[j] = true
				}
			}
		}
		for j := range candidates {
			if len(tokens[j]) < 2 {
				continue
			}
			key := [2]int{i, j}
			if seen[key] {
				continue
			}
			seen[key] = true
			if jaccard(tokens[i], tokens[j]) < 0.5 {
				continue
			}
			union(i, j)
		}
	}
	members := map[int][]partialentities.NameUses{}
	for i := range observed {
		root := find(i)
		members[root] = append(members[root], observed[i])
	}
	out := make([]partialentities.NameGroup, 0, len(members))
	oversizedFamilies, oversizedNames := 0, 0
	for _, group := range members {
		if len(group) < 2 {
			continue
		}
		if len(group) > maxNearDuplicateGroup {
			oversizedFamilies++
			oversizedNames += len(group)
			continue
		}
		sort.Slice(group, func(a, b int) bool { return group[a].Uses > group[b].Uses })
		out = append(out, partialentities.NameGroup{Reason: "shared-words", Names: group})
	}
	sortGroups(out)
	return out, oversizedFamilies, oversizedNames
}

// maxNearDuplicateGroup is the largest component a curation may be asked to merge. A larger
// component is a chain of broad words, not a synonym family: suggesting it would ask the model to
// merge unrelated subjects, and listing it would drown the report.
const maxNearDuplicateGroup = 12

// fallbackBroadTokens learns a broad-token set from FREQUENCY alone, for the case where no model is
// configured. It is deliberately data-driven: the words are chosen from this vault's names, never
// from a hardcoded language list.
func fallbackBroadTokens(observed []partialentities.NameUses) map[string]bool {
	df := map[string]int{}
	for _, o := range observed {
		for token := range distinctiveTokens(o.Name, nil) {
			df[token]++
		}
	}
	type count struct {
		token string
		n     int
	}
	counts := make([]count, 0, len(df))
	for token, n := range df {
		if n >= 4 {
			counts = append(counts, count{token: token, n: n})
		}
	}
	sort.SliceStable(counts, func(i, j int) bool {
		if counts[i].n != counts[j].n {
			return counts[i].n > counts[j].n
		}
		return counts[i].token < counts[j].token
	})
	if len(counts) > maxAutoBroadTokens {
		counts = counts[:maxAutoBroadTokens]
	}
	out := make(map[string]bool, len(counts))
	for _, c := range counts {
		out[c.token] = true
	}
	return out
}

const maxAutoBroadTokens = 400

func distinctiveTokens(name string, broad map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, token := range nameTokens(name) {
		if broad != nil && broad[token] {
			continue
		}
		out[token] = true
	}
	return out
}

func sortedTokenList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// nameTokens splits a name into the words it is made of ("cache-invalidation" → cache, invalidation).
func nameTokens(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == ' ' || r == '.'
	})
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	inter := 0
	for _, t := range b {
		if set[t] {
			inter++
		}
	}
	union := len(set) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// sortGroups orders the findings by size (the biggest first), then by the first name, so the report is
// stable from one read to the next.
func sortGroups(groups []partialentities.NameGroup) {
	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i].Names) != len(groups[j].Names) {
			return len(groups[i].Names) > len(groups[j].Names)
		}
		return groups[i].Names[0].Name < groups[j].Names[0].Name
	})
}
