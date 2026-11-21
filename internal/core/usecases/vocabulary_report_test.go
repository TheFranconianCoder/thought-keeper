package usecases

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// TestNearDuplicatesFindsNamesThatShareTheirWords: the free half of a curation. Two names that are
// the same words in another order are a question worth asking; the numbers never decide it, so this only
// has to be honest about what it offers.
func TestNearDuplicatesFindsNamesThatShareTheirWords(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "cache-invalidation", Uses: 9},
		{Name: "invalidation-cache", Uses: 2}, // same words, other order
		{Name: "caching", Uses: 40},
		{Name: "compilers", Uses: 7},
		{Name: "register-allocation", Uses: 3},
	}
	got, _, _ := nearDuplicates(observed, nil)
	if len(got) == 0 {
		t.Fatalf("no near-duplicate found in %v", observed)
	}
	found := false
	for _, g := range got {
		names := []string{g.Names[0].Name, g.Names[1].Name}
		for _, n := range names {
			if n == "compilers" || n == "register-allocation" || n == "caching" {
				t.Errorf("unrelated name in a group: %v", g)
			}
		}
		if (names[0] == "cache-invalidation" && names[1] == "invalidation-cache") ||
			(names[1] == "cache-invalidation" && names[0] == "invalidation-cache") {
			found = true
		}
	}
	if !found {
		t.Errorf("the same words in another order were not offered: %v", got)
	}
}

// TestJaccardIsTheShareOfSharedWords pins the measure itself: it is a threshold on a candidate, not a
// decision, so it only has to be predictable.
func TestJaccardIsTheShareOfSharedWords(t *testing.T) {
	if got := jaccard([]string{"cache", "invalidation"}, []string{"cache", "invalidation"}); got != 1 {
		t.Errorf("identical sets = %v, want 1", got)
	}
	if got := jaccard([]string{"cache", "invalidation"}, []string{"cache", "eviction"}); got != 1.0/3.0 {
		t.Errorf("one shared of three = %v, want 1/3", got)
	}
	if got := jaccard(nil, []string{"cache"}); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
}

// TestKeyOfCollapsesSpellings: two spellings of one name must land on one key — that is the finding that
// needs no judgement at all.
func TestKeyOfCollapsesSpellings(t *testing.T) {
	topics := entities.VocabularyTopics
	if keyOf("Cache Invalidation", topics) != keyOf("cache-invalidation", topics) {
		t.Error("two spellings of one name must share a key")
	}
	if keyOf("Caching", topics) != keyOf("caching", topics) {
		t.Error("case must not make two names")
	}
	if keyOf("Software Engineering/Caching", topics) != keyOf("software-engineering/caching", topics) {
		t.Error("a topic path must slugify to one key")
	}
	// A TAG is a short slug, not a path: the namespace decides how a name normalizes.
	if keyOf("Go Lang", entities.VocabularyWikiTags) != "go-lang" {
		t.Errorf("tag key = %q, want the slug", keyOf("Go Lang", entities.VocabularyWikiTags))
	}
}

func TestBroadTokensBreakGenericNameChains(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "go-http", Uses: 10},
		{Name: "go-language", Uses: 8},
		{Name: "rust-http", Uses: 7},
		{Name: "rust-language", Uses: 6},
		{Name: "cache-invalidation", Uses: 9},
		{Name: "invalidation-cache", Uses: 2},
	}
	broad := map[string]bool{"go": true, "rust": true, "http": true, "language": true}
	groups, oversized, _ := nearDuplicates(observed, broad)
	if oversized != 0 {
		t.Fatalf("unexpected oversized families: %d", oversized)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %v, want only the cache synonym pair", groups)
	}
	for _, g := range groups {
		for _, n := range g.Names {
			if n.Name == "go-http" || n.Name == "rust-http" || n.Name == "go-language" || n.Name == "rust-language" {
				t.Fatalf("a broad-token chain survived: %v", groups)
			}
		}
	}
}

func TestOversizedNearDuplicateComponentIsNotOffered(t *testing.T) {
	observed := make([]partialentities.NameUses, 0, 20)
	for i := range 20 {
		observed = append(observed, partialentities.NameUses{Name: "common-one-" + string(rune('a'+i)), Uses: 1})
	}
	groups, oversized, names := nearDuplicates(observed, nil)
	if len(groups) != 0 {
		t.Fatalf("an oversized chain was offered as merge candidates: %v", groups)
	}
	if oversized != 1 || names != 20 {
		t.Fatalf("oversized = %d/%d, want one family of 20 names", oversized, names)
	}
}

func TestParseBroadTokensKeepsOnlyCandidates(t *testing.T) {
	candidates := []tokenUse{{token: "go", uses: 100}, {token: "rust", uses: 80}}
	got := parseBroadTokens(`{"broad_tokens":["go","RUST","invented"]}`, candidates)
	if len(got) != 2 || got[0] != "go" || got[1] != "rust" {
		t.Fatalf("broad tokens = %v, want the two candidates lowercased", got)
	}
	if got := parseBroadTokens(`{"broad_tokens":["invented"]}`, candidates); len(got) != 0 {
		t.Fatalf("a token outside the candidate list must be ignored: %v", got)
	}
}

func TestFallbackBroadTokensComesFromTheCatalog(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "go-http", Uses: 1},
		{Name: "go-language", Uses: 1},
		{Name: "go-memory", Uses: 1},
		{Name: "go-modules", Uses: 1},
		{Name: "rust-http", Uses: 1},
		{Name: "rust-language", Uses: 1},
		{Name: "rust-memory", Uses: 1},
		{Name: "rust-modules", Uses: 1},
		{Name: "ts-http", Uses: 1},
		{Name: "js-http", Uses: 1},
		{Name: "cache-invalidation", Uses: 1},
	}
	broad := fallbackBroadTokens(observed)
	if !broad["go"] || !broad["rust"] || !broad["http"] {
		t.Fatalf("the frequent tokens were not learned: %v", broad)
	}
	if broad["cache"] {
		t.Fatal("a distinctive token was learned as broad")
	}
}

type broadGateway struct {
	answer string
	reqs   []interfaces.AgentRequest
}

func (g *broadGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.reqs = append(g.reqs, req)
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: g.answer}}, nil
}

func TestLearnBroadTokensUsesTheModelFromVaultCandidates(t *testing.T) {
	gw := &broadGateway{answer: `{"broad_tokens":["go","rust"]}`}
	uc := Vocabulary{Gateway: gw, Model: "test"}
	observed := []partialentities.NameUses{
		{Name: "go-http", Uses: 1},
		{Name: "go-language", Uses: 1},
		{Name: "go-memory", Uses: 1},
		{Name: "go-modules", Uses: 1},
		{Name: "rust-http", Uses: 1},
		{Name: "rust-language", Uses: 1},
		{Name: "rust-memory", Uses: 1},
		{Name: "rust-modules", Uses: 1},
		{Name: "cache-invalidation", Uses: 1},
	}
	got := uc.learnBroadTokens(context.Background(), observed)
	if len(got) != 2 || got[0] != "go" || got[1] != "rust" {
		t.Fatalf("broad tokens = %v, want go+rust from the model", got)
	}
	if len(gw.reqs) != 1 || !strings.Contains(gw.reqs[0].Messages[1].Content, "go") {
		t.Fatalf("the model did not receive the vault's candidate tokens: %+v", gw.reqs)
	}
}

// TestRemainingSkipsJudgedNames: the number a reader sees is the WORK, not a sentence served forever.
// A name the coarsening asked about and the shelf could not cover is judged, so it is no longer
// remaining — otherwise the automatic pass would be re-armed by its own refusals.
func TestRemainingSkipsJudgedNames(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "caching", Uses: 40},
		{Name: "cache-invalidation", Uses: 3}, // absorbed
		{Name: "tp-link-router", Uses: 2},     // judged and kept by the bulk pass
		{Name: "moral-psychology", Uses: 1},   // still a question
	}
	vocab := entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Shelf:     []string{"caching"},
		Absorbed:  map[string]string{"cache-invalidation": "caching"},
		Decided:   []string{"tp-link-router"},
	}
	uc := Vocabulary{}
	got := uc.health(observed, vocab, entities.VocabularyTopics)
	if got.Remaining != 1 {
		t.Errorf("remaining = %d, want 1 (only the name nobody has judged)", got.Remaining)
	}
	// A judged-and-kept name is not NEW either: an automatic curation must not be woken by names the
	// coarsening already answered for.
	if len(got.New) != 1 || got.New[0] != "moral-psychology" {
		t.Errorf("new = %v, want only the undecided name", got.New)
	}
	// The retopic queue is the CLAIMS' half of the same tail: both fine names, judged or not, because
	// "the shelf cannot take this name" is exactly when the claims have to be looked at one by one.
	if got.Retopic != 2 {
		t.Errorf("retopic = %d, want the 2 names outside the shelf", got.Retopic)
	}
	if got.OffShelf != 3 {
		t.Errorf("off_shelf = %d, want the 3 claims still carrying a fine name", got.OffShelf)
	}
	// A re-derived name leaves the retopic queue but stays in the off-shelf mass until its claims move:
	// the two numbers measure different things and only one of them can reach zero by asking.
	vocab.Relabelled = []string{"moral-psychology"}
	got = uc.health(observed, vocab, entities.VocabularyTopics)
	if got.Retopic != 1 || got.OffShelf != 3 {
		t.Errorf("retopic/off_shelf = %d/%d, want 1/3 after the pass", got.Retopic, got.OffShelf)
	}
}

// TestHealthJudgesTopicsByShare: a catch-all and a thin topic are read off the claim SHARES, so the same
// names are judged the same way in a vault of any size. The filler names only make the total realistic.
func TestHealthJudgesTopicsByShare(t *testing.T) {
	observed := make([]partialentities.NameUses, 0, 3+200)
	observed = append(observed,
		partialentities.NameUses{Name: "software", Uses: 3000},
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "rare-name", Uses: 1},
	)
	for i := range 200 {
		observed = append(observed, partialentities.NameUses{Name: fmt.Sprintf("filler-%03d", i), Uses: 500})
	}
	vocab := entities.Vocabulary{Namespace: entities.VocabularyTopics, Shelf: []string{"caching"}}
	uc := Vocabulary{Shelf: refitLimits}
	got := uc.health(observed, vocab, entities.VocabularyTopics)
	if got.CatchAll != 1 || got.CatchAllUses != 3000 {
		t.Errorf("catch-all = %d/%d, want 1/3000 (software is %.1f%% of the claims)",
			got.CatchAll, got.CatchAllUses, 100*3000/float64(got.Uses))
	}
	if got.Thin != 1 || got.ThinUses != 1 {
		t.Errorf("thin = %d/%d, want the singleton alone", got.Thin, got.ThinUses)
	}

	wiki := uc.health(observed, entities.Vocabulary{Namespace: entities.VocabularyWikiTags}, entities.VocabularyWikiTags)
	if wiki.CatchAll != 0 || wiki.Thin != 0 {
		t.Errorf("tag namespaces are not judged by share: catch-all %d thin %d", wiki.CatchAll, wiki.Thin)
	}
}
