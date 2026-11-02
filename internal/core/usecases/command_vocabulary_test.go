package usecases

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// The vocabulary the assistant matches against is the vault's own (a real export, abridged).
func realVocabulary() map[string]int {
	return map[string]int{
		"open-source":            133,
		"software-craftsmanship": 116,
		"go":                     115,
		"testing":                89,
		"tooling":                76,
		"software-design":        74,
		"software-architecture":  67,
		"web-development":        64,
		"systems-engineering":    58,
		"agile-development":      53,
		"performance":            52,
		"programming-languages":  52,
	}
}

// "Software Architektur" must land on software-architecture — and NOT on software-design or every
// other software-* tag. That is what inverse document frequency is for: "software" is in three
// names and identifies none of them, "architecture" is in one.
func TestMatchNamesPrefersTheSpecificWord(t *testing.T) {
	got := matchNames([]string{"software", "architecture", "architektur"}, realVocabulary(), nil, 0)
	if len(got) == 0 {
		t.Fatal("nothing matched")
	}
	if got[0].Name != "software-architecture" {
		t.Fatalf("first match = %q, want software-architecture (got %+v)", got[0].Name, got)
	}
}

// The same word in another language is a MATCH, not a coincidence: "Architektur" and "architecture"
// are two edits apart, and a loanword pair like that is exactly what the matcher should catch.
func TestMatchNamesBridgesLoanwords(t *testing.T) {
	got := matchNames([]string{"architektur"}, realVocabulary(), nil, 0)
	if len(got) == 0 || got[0].Name != "software-architecture" {
		t.Fatalf("a German loanword did not reach its English name: %+v", got)
	}
}

// Recall must not become noise: an unrelated word matches nothing, however it would be spelled.
func TestMatchNamesKeepsUnrelatedWordsOut(t *testing.T) {
	for _, term := range []string{"urlaub", "quux", "zzz"} {
		for _, m := range matchNames([]string{term}, realVocabulary(), nil, 0) {
			t.Fatalf("%q matched %q", term, m.Name)
		}
	}
}

// The three similarity rules, each on its own.
func TestWordSimilarityHelpers(t *testing.T) {
	if stem("architectures") != "architecture" && stem("architectures") != "architectur" {
		// "-s" and "-es" both strip; the point is that it settles on one form.
		if stem(stem("architectures")) != stem("architecture") {
			t.Fatalf("plural not normalised: %q vs %q", stem("architectures"), stem("architecture"))
		}
	}
	if levenshtein("kubernetes", "kubernetis") != 1 {
		t.Fatalf("one typo was not seen as one")
	}
	if koelner("Müller") != koelner("Miller") || koelner("Schmidt") != koelner("Schmitt") {
		t.Fatalf("phonetics: Müller=%s Miller=%s Schmidt=%s Schmitt=%s",
			koelner("Müller"), koelner("Miller"), koelner("Schmidt"), koelner("Schmitt"))
	}
}

// A curated merge is a synonym: a request that still uses the absorbed name must land on the
// canonical one.
func TestExpandAliases(t *testing.T) {
	uc := Commands{Vocab: fakeVocab{m: map[entities.VocabularyNamespace]entities.Vocabulary{
		entities.VocabularySourceTags: {
			Namespace: entities.VocabularySourceTags,
			Absorbed:  map[string]string{"cloud-computing": "cloud"},
		},
	}}}
	got := uc.expandAliases([]string{"cloud-computing"})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "cloud") {
		t.Fatalf("the canonical name behind an alias was not added: %v", got)
	}
}

type fakeVocab struct {
	m map[entities.VocabularyNamespace]entities.Vocabulary
}

func (f fakeVocab) Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error) {
	return f.m[ns], nil
}
func (f fakeVocab) Write(entities.Vocabulary) error { return nil }

// The tag a human put on a source and the topic an agent gave a claim are matched the same way.
func TestMatchNamesHandlesHyphenatedNames(t *testing.T) {
	got := matchNames([]string{"software architecture"}, realVocabulary(), nil, 0)
	if len(got) == 0 || got[0].Name != "software-architecture" {
		t.Fatalf("a hyphenated name was not matched: %+v", got)
	}
	got = matchNames([]string{"tooling"}, realVocabulary(), nil, 0)
	if len(got) == 0 || got[0].Name != "tooling" {
		t.Fatalf("an exact name was not matched: %+v", got)
	}
}

// Nothing in the vocabulary → nothing. An honest empty answer beats a vague one.
func TestMatchNamesReturnsNothingForNonsense(t *testing.T) {
	if got := matchNames([]string{"quux", "zzz"}, realVocabulary(), nil, 0); len(got) != 0 {
		t.Fatalf("nonsense matched %+v", got)
	}
}

// A shared token in two multi-word names is not a name: "machine-learning" must not drag in the
// generic tag "learning", and "artificial-intelligence" must not drag in "intelligence-community".
func TestMatchNamesRejectsTheGenericNameTrap(t *testing.T) {
	names := map[string]int{
		"learning":                64,
		"machine-learning":        15,
		"intelligence-community":  4,
		"artificial-intelligence": 4,
	}
	got := matchNames([]string{"machine-learning"}, names, nil, 0)
	if len(got) != 1 || got[0].Name != "machine-learning" {
		t.Fatalf("machine-learning matched %+v, want only the compound name", got)
	}
	got = matchNames([]string{"artificial-intelligence"}, names, nil, 0)
	if len(got) != 1 || got[0].Name != "artificial-intelligence" {
		t.Fatalf("artificial-intelligence matched %+v, want only the compound name", got)
	}
}

// A name the vault uses for a large part of itself is a field, not an answer — unless the reader
// named its words. "agile" must not drag in "agile-development"; "open source" may keep it.
func TestMatchNamesRefusesBroadNamesUnlessNamed(t *testing.T) {
	names := map[string]int{
		"agile":             141,
		"agile-development": 417,
		"agile-coaching":    54,
		"open-source":       138,
	}
	got := matchNames([]string{"agile", "agile-development"}, names, map[string]bool{"agile": true}, 1554)
	for _, m := range got {
		if m.Name == "agile-development" {
			t.Fatalf("a broad name was kept without being named: %+v", got)
		}
	}
	got = matchNames([]string{"open-source"}, names, map[string]bool{"open-source": true}, 1554)
	found := false
	for _, m := range got {
		if m.Name == "open-source" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a name the reader named was dropped: %+v", got)
	}
}

// The request's own words are the no-model fallback: they still find the tag when the wording agrees.
func TestMeaningfulWordsDropsTheSentence(t *testing.T) {
	got := meaningfulWords("Zeige mir Quellen über Software Architektur")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "software") || !strings.Contains(joined, "architektur") {
		t.Fatalf("words = %v", got)
	}
	for _, word := range []string{"zeige", "mir", "quellen", "über"} {
		if strings.Contains(" "+joined+" ", " "+word+" ") {
			t.Fatalf("%q is sentence glue, not a search word: %v", word, got)
		}
	}
}
