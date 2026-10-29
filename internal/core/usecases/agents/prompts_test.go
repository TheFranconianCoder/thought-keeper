package agents_test

import (
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
)

// TestDistillClaimsAreEnglish: a claim is English whatever the source's language. Proper names,
// titles, code and quoted terms keep their script; the sentence around them does not.
func TestDistillClaimsAreEnglish(t *testing.T) {
	for _, want := range []string{
		"topic, claim and body in ENGLISH",
		"regardless of the source's language",
		"ENGLISH topic",
	} {
		if !strings.Contains(agents.DistillSystem, want) {
			t.Errorf("DistillSystem lacks %q", want)
		}
	}
	if strings.Contains(agents.DistillSystem, "source's own language") {
		t.Error("DistillSystem still writes claims in the source's language")
	}
}

// TestQueryPromptKeepsTheAnswerInTheQuestionLanguage: the answer language is the question's prose,
// not the language of the claims, the vault or a proper noun inside the question. A German question
// that names a Chinese source is answered in German.
func TestQueryPromptKeepsTheAnswerInTheQuestionLanguage(t *testing.T) {
	for _, want := range []string{
		"ENTIRELY in the language of the QUESTION",
		"Chinese source title inside a German question",
		"question mixes languages",
	} {
		if !strings.Contains(agents.QuerySystem, want) {
			t.Errorf("QuerySystem lacks %q", want)
		}
	}
	if strings.Contains(agents.QuerySystem, "Search and answer across them") {
		t.Error("QuerySystem still tells the model to answer across the vault's languages")
	}

	user := agents.QueryUser("Was weißt du über die Quelle 紫微斗数 · 开源排盘引擎?", []string{"zh", "en"}, time.Time{})
	for _, want := range []string{
		"the ANSWER stays in the question's language",
		"entirely in the language of the question above",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("QueryUser lacks %q", want)
		}
	}
}

// TestQueryPromptHasTheRecencyBranch: a digest question must not start with a claim search, and the
// window must be vault arrival. The clock is in the user message so the model can resolve "the last
// 24 hours" without guessing today's date.
func TestQueryPromptHasTheRecencyBranch(t *testing.T) {
	for _, want := range []string{
		"list_sources",
		"read_source_summary",
		"ENTERED THE VAULT",
		"never \"published\"",
		"next_offset",
		"[[source:<hash>|<source title>]]",
		"claim finder query",
	} {
		if !strings.Contains(agents.QuerySystem, want) {
			t.Errorf("QuerySystem lacks %q", want)
		}
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	user := agents.QueryUser("Fasse die jüngsten Sources der letzten 24h zusammen.", nil, now)
	if !strings.Contains(user, "Current time: 2026-10-28T12:00:00Z") {
		t.Errorf("QueryUser lacks the current time: %q", user)
	}
}
