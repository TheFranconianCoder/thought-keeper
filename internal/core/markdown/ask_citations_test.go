package markdown

import (
	"strings"
	"testing"
)

const (
	citeA = "01J8ZC5V9P0R7T2QK4W6Y8B3ND"
	citeB = "01J8ZC5V9P0R7T2QK4W6Y8B3NE"
)

func TestNormalizeAskAnswerUsesNumberedReferencesAndABibliography(t *testing.T) {
	in := "Prose [[claim:" + citeA + "|Caching is subtle]].\n\nSources:\n- [[claim:" + citeA + "|Caching is subtle]]"
	out := NormalizeAskAnswer(in)
	if strings.Contains(out, "[[claim:") {
		t.Fatalf("claim syntax survived: %q", out)
	}
	if !strings.Contains(out, "Prose ([1](#cite-1)).") {
		t.Errorf("prose citation is not a numbered reference in parentheses: %q", out)
	}
	if !strings.Contains(out, "Sources:\n1. [Caching is subtle](#/claims/"+citeA+")") {
		t.Errorf("the bibliography entry is missing or unnumbered: %q", out)
	}
	if strings.Count(out, "](#/claims/"+citeA+")") != 1 {
		t.Errorf("want one prose reference and one bibliography entry: %q", out)
	}
}

func TestNormalizeAskAnswerKeepsEverySourceInTheBibliography(t *testing.T) {
	in := "Prose [[claim:" + citeA + "|Alpha]].\n\nSources:\n- [[claim:" + citeA + "|Alpha]]\n- [[claim:" + citeB + "|Beta]]"
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "Sources:") {
		t.Fatalf("the bibliography disappeared: %q", out)
	}
	if !strings.Contains(out, "Prose ([1](#cite-1)).") {
		t.Errorf("Alpha's prose reference is wrong: %q", out)
	}
	if !strings.Contains(out, "1. [Alpha](#/claims/"+citeA+")") {
		t.Errorf("Alpha is missing from the bibliography: %q", out)
	}
	if !strings.Contains(out, "2. [Beta](#/claims/"+citeB+")") {
		t.Errorf("Beta is missing from the bibliography: %q", out)
	}
}

func TestNormalizeAskAnswerRepairsAnAlreadyNormalizedProsePlaceholder(t *testing.T) {
	in := "Prose ([\\[Caching is subtle\\]](#/claims/" + citeA + ")).\n\nSources:\n- [claim](#/claims/" + citeA + ")"
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "Prose ([1](#cite-1)).") {
		t.Errorf("prose link was not repaired: %q", out)
	}
	if !strings.Contains(out, "1. [Caching is subtle](#/claims/"+citeA+")") {
		t.Errorf("the label was not restored in the bibliography: %q", out)
	}
}

func TestNormalizeAskAnswerWrapsAnOldPlainProseLinkInParentheses(t *testing.T) {
	in := "Prose [Caching is subtle](#/claims/" + citeA + ")."
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "Prose ([1](#cite-1)).") {
		t.Errorf("old prose link was not turned into a reference: %q", out)
	}
	if !strings.Contains(out, "1. [Caching is subtle](#/claims/"+citeA+")") {
		t.Errorf("the old label was not carried into the bibliography: %q", out)
	}
}

func TestNormalizeAskAnswerAddsParenthesesWhenTheModelOmittedThem(t *testing.T) {
	in := "Prose [[claim:" + citeA + "|Caching is subtle]]."
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "Prose ([1](#cite-1)).") {
		t.Errorf("citation was not wrapped: %q", out)
	}
}

func TestNormalizeAskAnswerNumbersRepeatedCitationsOnce(t *testing.T) {
	in := "One [[claim:" + citeA + "|Alpha]], again [[claim:" + citeA + "|Alpha]]."
	out := NormalizeAskAnswer(in)
	if strings.Count(out, "([1](#cite-1))") != 2 {
		t.Errorf("a repeated claim should reuse its number: %q", out)
	}
	if strings.Count(out, "1. [Alpha](#/claims/"+citeA+")") != 1 {
		t.Errorf("the bibliography should list the claim once: %q", out)
	}
}

func TestNormalizeAskAnswerNumbersCitationsInFirstAppearanceOrder(t *testing.T) {
	in := "One [[claim:" + citeA + "|Alpha]], two [[claim:" + citeB + "|Beta]]."
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "One ([1](#cite-1)), two ([2](#cite-2)).") {
		t.Errorf("references are not numbered in prose order: %q", out)
	}
	if !strings.Contains(out, "1. [Alpha](#/claims/"+citeA+")") || !strings.Contains(out, "2. [Beta](#/claims/"+citeB+")") {
		t.Errorf("bibliography order is wrong: %q", out)
	}
}

func TestNormalizeAskAnswerFallsBackToTheIDWithoutALabel(t *testing.T) {
	in := "Sources:\n- [[claim:" + citeA + "]]"
	out := NormalizeAskAnswer(in)
	if strings.Contains(out, `[claim](#/claims/`) {
		t.Errorf("unlabelled citation fell back to the placeholder: %q", out)
	}
	if !strings.Contains(out, "1. ["+citeA+"](#/claims/"+citeA+")") {
		t.Errorf("unlabelled citation did not fall back to its id: %q", out)
	}
}

func TestNormalizeAskAnswerKeepsParenthesesAndDoesNotDoubleThem(t *testing.T) {
	in := "Prose ([[claim:" + citeA + "|Caching is subtle]])."
	out := NormalizeAskAnswer(in)
	wantProse := "Prose ([1](#cite-1))."
	if !strings.HasPrefix(out, wantProse) {
		t.Fatalf("out = %q, want prose %q", out, wantProse)
	}
	if strings.Contains(out, "((") {
		t.Fatalf("citation parentheses were doubled: %q", out)
	}
}

func TestNormalizeAskAnswerIsIdempotentForNumberedAsks(t *testing.T) {
	in := "Prose ([1](#cite-1)).\n\nSources:\n1. [Caching is subtle](#/claims/" + citeA + ")"
	out := NormalizeAskAnswer(in)
	if out != in {
		t.Fatalf("numbered ask changed on a second pass:\n got %q\nwant %q", out, in)
	}
}

func TestNormalizeAskAnswerNumbersSourceCitations(t *testing.T) {
	hash := strings.Repeat("a1", 32)
	in := "Prose [[source:" + hash + "|A recent article]]."
	out := NormalizeAskAnswer(in)
	if strings.Contains(out, "[[source:") {
		t.Fatalf("source syntax survived: %q", out)
	}
	if !strings.Contains(out, "Prose ([1](#cite-1)).") {
		t.Errorf("source citation is not a numbered reference: %q", out)
	}
	if !strings.Contains(out, "1. [A recent article](#/sources/"+hash+")") {
		t.Errorf("the source bibliography entry is missing or wrong: %q", out)
	}
}

func TestNormalizeAskAnswerMixesClaimsAndSources(t *testing.T) {
	hash := strings.Repeat("b2", 32)
	in := "Claim [[claim:" + citeA + "|Alpha]], source [[source:" + hash + "|A recent article]]."
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "Claim ([1](#cite-1)), source ([2](#cite-2)).") {
		t.Errorf("references are not numbered in prose order: %q", out)
	}
	if !strings.Contains(out, "1. [Alpha](#/claims/"+citeA+")") {
		t.Errorf("the claim is missing from the bibliography: %q", out)
	}
	if !strings.Contains(out, "2. [A recent article](#/sources/"+hash+")") {
		t.Errorf("the source is missing from the bibliography: %q", out)
	}
}

func TestNormalizeAskAnswerKeepsASourceBibliographyOnASecondPass(t *testing.T) {
	hash := strings.Repeat("c3", 32)
	in := "Prose ([1](#cite-1)).\n\nSources:\n1. [A recent article](#/sources/" + hash + ")"
	out := NormalizeAskAnswer(in)
	if out != in {
		t.Fatalf("numbered ask changed on a second pass:\n got %q\nwant %q", out, in)
	}
}

func TestNormalizeAskAnswerFallsBackToTheSourceHashWithoutATitle(t *testing.T) {
	hash := strings.Repeat("d4", 32)
	out := NormalizeAskAnswer("Sources:\n- [[source:" + hash + "]]")
	if strings.Contains(out, `[source](#/sources/`) {
		t.Errorf("unlabelled source fell back to the placeholder: %q", out)
	}
	if !strings.Contains(out, "1. ["+hash+"](#/sources/"+hash+")") {
		t.Errorf("unlabelled source did not fall back to its hash: %q", out)
	}
}

func TestNormalizeAskAnswerDemotesABareWikilinkToText(t *testing.T) {
	in := "Prose [[The other Rust features]] and [[Another title|Alias]]."
	out := NormalizeAskAnswer(in)
	if strings.Contains(out, "[[") {
		t.Errorf("bare wikilink survived: %q", out)
	}
	if !strings.Contains(out, "Prose The other Rust features and Alias.") {
		t.Errorf("bare wikilinks were not demoted to text: %q", out)
	}
	if strings.Contains(out, "#/claims?q=") {
		t.Errorf("a claim finder link was produced: %q", out)
	}
}

func TestNormalizeAskAnswerKeepsAWikiLink(t *testing.T) {
	in := "See [[wiki:01J8ABC|A page]]."
	out := NormalizeAskAnswer(in)
	if !strings.Contains(out, "[[wiki:01J8ABC|A page]]") {
		t.Errorf("wiki link was changed: %q", out)
	}
}
