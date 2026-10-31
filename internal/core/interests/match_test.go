package interests

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func rule(terms ...string) entities.Interest {
	return entities.Interest{Terms: terms, Enabled: true}
}

func source(title string, topics []string, claims ...string) SourceText {
	out := SourceText{Title: title}
	for i, claim := range claims {
		topic := ""
		if i < len(topics) {
			topic = topics[i]
		}
		out.Claims = append(out.Claims, ClaimText{Topic: topic, Claim: claim})
	}
	return out
}

func match(in entities.Interest, src SourceText) Match {
	return Evaluate(in, ForSource(src, in))
}

func TestTwoMentionsAreAThemeAndOneIsASuggestion(t *testing.T) {
	src := source("A scheduling note", nil,
		"The tokio scheduler is cooperative.",
		"The tokio runtime uses a work-stealing scheduler.",
	)
	if got := match(rule("tokio"), src); got.Band != BandFits {
		t.Fatalf("two mentions = %q, want fits (%+v)", got.Band, got)
	}

	one := source("A scheduling note", nil, "The tokio scheduler is cooperative.")
	if got := match(rule("tokio"), one); got.Band != BandMaybe {
		t.Fatalf("one mention = %q, want maybe", got.Band)
	}
}

func TestAClaimTopicIsAStrongHit(t *testing.T) {
	src := source("A note", []string{"async-runtime"}, "The runtime is cooperative.")
	got := match(rule("async"), src)
	if got.Band != BandFits {
		t.Fatalf("topic hit = %q, want fits", got.Band)
	}
	if len(got.Topics) != 1 || got.Topics[0] != "async-runtime" {
		t.Fatalf("topics = %v, want the matched topic", got.Topics)
	}
}

func TestTitleAndLabelAreStrongHits(t *testing.T) {
	if got := match(rule("rust"), SourceText{Title: "The Rust async book"}); got.Band != BandFits {
		t.Fatalf("title hit = %q, want fits", got.Band)
	}
	if got := match(rule("rust"), SourceText{Tags: []string{"rust"}}); got.Band != BandFits {
		t.Fatalf("tag hit = %q, want fits", got.Band)
	}
}

func TestExclusionVetoesTheMatch(t *testing.T) {
	in := rule("rust")
	in.Exclude = []string{"corrosion"}
	src := source("Rust and corrosion", nil, "Rust is a corrosion process.", "Rust forms on iron.")
	if got := match(in, src); got.Band != "" {
		t.Fatalf("excluded source = %q, want no match", got.Band)
	}
}

func TestInflectionAndPhrases(t *testing.T) {
	src := source("Caching", nil, "Cache invalidation is hard.", "A cache needs a policy.")
	if got := match(rule("caching"), src); got.Band != BandFits {
		t.Fatalf("caching against cache = %q, want fits", got.Band)
	}
	src = source("Machine learning", nil, "Machine learning needs data.", "Learning machines are different.")
	if got := match(rule("machine learning"), src); got.Band != BandFits {
		t.Fatalf("phrase = %q, want fits", got.Band)
	}
}

func TestRustDoesNotMatchRussia(t *testing.T) {
	src := source("Russia", nil, "Russia is large.", "Russia has many regions.")
	if got := match(rule("rust"), src); got.Band != "" {
		t.Fatalf("rust matched Russia: %+v", got)
	}
}

func TestEvidenceNamesTheTerm(t *testing.T) {
	src := source("Tokio", []string{"async-runtime"}, "Tokio schedules tasks.", "Tokio is a runtime.")
	got := match(rule("tokio"), src)
	if len(got.Terms) != 1 || got.Terms[0].Term != "tokio" {
		t.Fatalf("evidence = %+v", got.Terms)
	}
	if got.Terms[0].Claims != 2 {
		t.Fatalf("claims = %d, want 2", got.Terms[0].Claims)
	}
}
