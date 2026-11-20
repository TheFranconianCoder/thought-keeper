package usecases

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func catalogOf(names ...string) *topicCatalog {
	topics := make([]entities.TopicUse, 0, len(names))
	for i, n := range names {
		topics = append(topics, entities.TopicUse{Topic: n, Count: len(names) - i})
	}
	return newTopicCatalog(topics)
}

// TestTopicCandidatesFollowTheText: a part is offered the names that share its RARE words. A name
// that shares only a word living in a tenth of the catalog ("system") says nothing about a subject,
// and the catalog drops it — that is what keeps the list short and useful.
func TestTopicCandidatesFollowTheText(t *testing.T) {
	c := catalogOf("caching", "cache-invalidation", "llm-agents", "postgresql", "rust-ownership",
		"system-design", "system-architecture", "system-monitoring", "system-thinking",
		"system-boundaries", "system-failure", "system-observability", "system-cost",
		"system-throughput", "system-latency", "system-capacity", "system-testing")
	got := c.candidates("This part explains how a cache invalidation key is chosen.", 5)
	if len(got) == 0 || (got[0] != "cache-invalidation" && got[0] != "caching") {
		t.Fatalf("candidates = %v, want a cache name first", got)
	}
	for _, name := range got {
		if name == "rust-ownership" || name == "postgresql" {
			t.Errorf("a candidate about an unrelated subject: %q in %v", name, got)
		}
	}
	// The over-used word is dropped from the index: "system" matches nothing.
	if got := c.candidates("a system of systems", 5); len(got) != 0 {
		t.Errorf("the common token matched %v, want nothing", got)
	}
}

// TestTopicResolveKeepsTheCatalogClosed: an alias wins, a spelling of an existing subject is mapped
// onto it, and a genuinely new subject still gets its own name while there is room.
func TestTopicResolveKeepsTheCatalogClosed(t *testing.T) {
	c := catalogOf("cache-invalidation", "caching")
	c.budget = 10

	if got := c.resolve(map[string]string{"cache-keys": "cache-invalidation"}, "cache-keys", ""); got.Name != "cache-invalidation" || !got.Mapped || got.Why != "alias" {
		t.Errorf("alias resolution = %+v", got)
	}
	// A name of two words plus a modifier is a spelling of the shorter existing name.
	if got := c.resolve(nil, "cache-invalidation-strategy", ""); got.Name != "cache-invalidation" || got.Why != "near-duplicate" {
		t.Errorf("near-duplicate resolution = %+v", got)
	}
	// A single shared word is NOT enough: "caching" and "cache-invalidation" are two subjects, in
	// both directions.
	if got := c.resolve(nil, "caching-invalidation", ""); got.Mapped {
		t.Errorf("a new subject was mapped: %+v", got)
	}
	if got := c.resolve(nil, "caching-strategies", "caching strategies"); got.Mapped {
		t.Errorf("a new subject was mapped: %+v", got)
	}
	// A name the catalog already carries is kept as it is.
	if got := c.resolve(nil, "caching", ""); got.Name != "caching" || got.Mapped {
		t.Errorf("existing name = %+v", got)
	}
}

// TestTopicResolveHonoursTheBudget: once the ceiling is reached, a new name is mapped onto the best
// fitting one instead of growing the catalog — a vocabulary can only expand by making room.
func TestTopicResolveHonoursTheBudget(t *testing.T) {
	c := catalogOf("caching", "llm-agents")
	c.budget = 2
	got := c.resolve(nil, "agent-orchestration", "an agent orchestration runtime for llm agents")
	if !got.Mapped || got.Name != "llm-agents" {
		t.Errorf("budget resolution = %+v, want llm-agents", got)
	}
}

// TestAuditSamplingIsDeterministic: the same part always gets the same answer, and the sample is
// one part in n — a re-derived part is not audited by luck.
func TestAuditSamplingIsDeterministic(t *testing.T) {
	uc := Derivation{AuditEvery: 4}
	hit, miss := 0, 0
	for _, id := range []string{"0.1", "0.2", "1.1", "2.3.4", "A", "B", "C", "D"} {
		first := uc.auditsPart(id)
		if first != uc.auditsPart(id) {
			t.Fatalf("part %q is not sampled deterministically", id)
		}
		if first {
			hit++
		} else {
			miss++
		}
	}
	if hit == 0 || miss == 0 {
		t.Errorf("sample is not spread: %d hit, %d miss", hit, miss)
	}
	if !(Derivation{}).auditsPart("x") {
		t.Error("an unset sample must audit every part")
	}
	if !(Derivation{AuditEvery: 1}).auditsPart("x") {
		t.Error("AuditEvery=1 must audit every part")
	}
}

// TestAuditWorthwhileCoversTheEmptyAndTheThin: a part with no claim is always checked (that is how
// metadata is told apart from a failed derivation), and so is a long text that produced almost
// nothing.
func TestAuditWorthwhileCoversTheEmptyAndTheThin(t *testing.T) {
	if !auditWorthwhile(false, 500, 0) {
		t.Error("a part with no claim must be audited")
	}
	if !auditWorthwhile(false, 6000, 1) {
		t.Error("a 6k text with one claim must be audited")
	}
	if auditWorthwhile(false, 6000, 8) {
		t.Error("a 6k text with eight claims is dense enough")
	}
	if !auditWorthwhile(true, 6000, 8) {
		t.Error("a sampled part is audited whatever it wrote")
	}
}
