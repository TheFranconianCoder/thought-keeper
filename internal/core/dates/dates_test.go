package dates_test

import (
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/dates"
)

// TestParseReadsWhatAPageDeclares: the value of a meta tag is a date in whatever form the publisher
// wrote it. The European numeric form is read day-first, and an impossible date is refused rather
// than normalised into a neighbour.
func TestParseReadsWhatAPageDeclares(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-10-27", "2026-10-27"},
		{"2026-10-27T05:36:09Z", "2026-10-27T05:36:09Z"},
		{"2026-10-27T07:36:09+02:00", "2026-10-27T05:36:09Z"},
		{"2026-10-27 07:36", "2026-10-27T07:36:00Z"},
		{"27.10.2026", "2026-10-27"},
		{"2.1.2026", "2026-01-02"},
		{"2. Januar 2026", "2026-01-02"},
		{"27. Oktober 2026", "2026-10-27"},
		{"2. März 2026", "2026-03-02"},
		{"January 2, 2026", "2026-01-02"},
		{"Oct 27, 2026", "2026-10-27"},
		{"27 Oct 2026", "2026-10-27"},
		{"published 2026-10-27", "2026-10-27"},
		{"", ""},
		{"31.02.2026", ""},
		{"27.13.2026", ""},
		{"no date here", ""},
	}
	for _, c := range cases {
		got, ok := dates.Parse(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("Parse(%q) = %s, want no date", c.in, got.Format(time.RFC3339))
			}
			continue
		}
		if !ok {
			t.Errorf("Parse(%q) found nothing, want %s", c.in, c.want)
			continue
		}
		layout := time.RFC3339
		if len(c.want) == len("2006-01-02") {
			layout = "2006-01-02"
		}
		if got.UTC().Format(layout) != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, got.UTC().Format(layout), c.want)
		}
	}
}

// TestFromTextOnlyTrustsTheHeadOrALabel: a date under the title is the article's date, and a date
// anywhere else counts only when the prose says it is the publication. A date mentioned deep in the
// text is a fact in the article, not its date.
func TestFromTextOnlyTrustsTheHeadOrALabel(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	head := "27. Oktober 2026\n\nDer Cache hält Kopien nah."
	if got, ok := dates.FromText(head, now); !ok || got.Format("2006-01-02") != "2026-10-27" {
		t.Fatalf("head date = %v (%v), want 2026-10-27", got, ok)
	}

	deep := "Ein langer Text über Caching, der weit über die Überschrift hinausgeht und sehr viele " +
		"Worte macht, bevor er endlich zur Sache kommt. Er erklärt zuerst, was ein Cache ist, dann, " +
		"warum er schneller macht, und erst danach: Vor vielen Jahren, am 3. März 2019, geschah " +
		"etwas anderes, das niemand mehr erklären konnte."
	if _, ok := dates.FromText(deep, now); ok {
		t.Fatal("a date deep in the text must not be the article's date")
	}

	labelled := "Ein langer Text über Caching, der erst später zur Sache kommt. " +
		"Veröffentlicht am 3. März 2019."
	if got, ok := dates.FromText(labelled, now); !ok || got.Format("2006-01-02") != "2019-03-03" {
		t.Fatalf("labelled date = %v (%v), want 2019-03-03", got, ok)
	}

	// An update is not a publication: the prose names it, and the name does not count as one.
	updated := "Ein langer Text über Caching, der weit über die Überschrift hinausgeht und sehr viele " +
		"Worte macht, bevor er endlich zur Sache kommt. Er erklärt zuerst, was ein Cache ist, dann, " +
		"warum er schneller macht, und erst danach: Updated on 3 March 2019."
	if _, ok := dates.FromText(updated, now); ok {
		t.Fatal("an update date deep in the text must not be read as publication")
	}

	// A date in the far future is a template placeholder, not a publication.
	if _, ok := dates.FromText("Veröffentlicht am 3. März 2099.", now); ok {
		t.Fatal("a far-future date must be refused")
	}
}
