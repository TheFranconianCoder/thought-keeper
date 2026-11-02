package usecases

import (
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// The parser is the no-model path and the fallback: these are the shapes people actually type, in
// both shipped languages.
func TestParseCommand(t *testing.T) {
	cases := []struct {
		text  string
		lens  entities.CommandLens
		route string
		check func(t *testing.T, c entities.CommandCriteria)
	}{
		{
			// "Thema" is a TOPIC, not a tag — the parser says so, and RunCommand turns it into the
			// provenance join (see TestReconcileCriteriaMovesTopicToProvenance).
			text: "zeige mir alle quellen zum thema cloud",
			lens: entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "topic", c.Topic, "cloud")
				same(t, "tag", c.Tag, "")
			},
		},
		{
			text:  "Quellen mit Tag cloud",
			lens:  entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) { same(t, "tag", c.Tag, "cloud") },
		},
		{
			text:  "zeige mir alle quellen, die ich letzte woche hinzugefügt habe und die noch nicht destilliert sind",
			lens:  entities.CommandSources,
			route: "filter",
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "state", string(c.State), string(entities.CommandStateOpen))
				same(t, "window", c.Window.Spec, "last-week")
				same(t, "text", c.Text, "")
			},
		},
		{
			text:  "Sources der letzten 2 Tage",
			lens:  entities.CommandSources,
			route: "filter",
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "window", c.Window.Spec, "last-2d")
			},
		},
		{
			// A question-shaped LIST request is still a list: "what do I have from X?" names
			// words, not a request for prose.
			text:  "Was haben wir von DHH?",
			lens:  entities.CommandSources,
			route: "filter",
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "text", c.Text, "DHH")
			},
		},
		{
			text:  "Quellen von Alistair Cockburn",
			lens:  entities.CommandSources,
			route: "filter",
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "text", c.Text, "Alistair Cockburn")
			},
		},
		{
			text:  "show me sourecs tagged cloud added this week",
			lens:  entities.CommandSources,
			route: "filter",
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "tag", c.Tag, "cloud")
				same(t, "window", c.Window.Spec, "this-week")
				same(t, "text", c.Text, "")
			},
		},
		{
			text:  "Was sagen die Quellen über Remote Work?",
			lens:  entities.CommandAsk,
			route: "ask",
			check: func(t *testing.T, c entities.CommandCriteria) {
				if c.Text == "" {
					t.Fatal("a question keeps its wording")
				}
			},
		},
		{
			text:  "claims über caching",
			lens:  entities.CommandClaims,
			check: func(t *testing.T, c entities.CommandCriteria) { same(t, "topic", c.Topic, "caching") },
		},
		{
			// "Which sources failed?" is the Attention list, not a request for prose.
			text:  "welche quellen sind fehlgeschlagen?",
			lens:  entities.CommandAttention,
			route: "filter",
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "state", string(c.State), string(entities.CommandStateFailed))
			},
		},
		{
			text:  "zeige mir alle pdf quellen",
			lens:  entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) { same(t, "kind", c.Kind, "pdf") },
		},
		{
			text: "lesezeichen",
			lens: entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) {
				if !c.BookmarksOnly {
					t.Fatal("bookmarks was not read")
				}
			},
		},
		{
			text: "quellen von heise.de",
			lens: entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) {
				// The host is free text: the filter matches the URL, which is exactly the query it
				// needs — and the record shows it as the text it is.
				if c.Text != "heise.de" {
					t.Fatalf("text = %q, want heise.de", c.Text)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			parsed, err := ParseCommand(tc.text, CommandRefs{})
			if err != nil {
				t.Fatalf("ParseCommand: %v", err)
			}
			same(t, "lens", string(parsed.Lens), string(tc.lens))
			if tc.route != "" {
				same(t, "route", parsed.Route, tc.route)
			}
			tc.check(t, parsed.Criteria)
		})
	}
}

// A model (or the rules) may put a subject in the claims field of a SOURCE request. That is not a
// reason to drop it: it is translated into the provenance join, and the translation is recorded.
func TestReconcileCriteriaMovesTopicToProvenance(t *testing.T) {
	parsed := ParsedCommand{
		Lens:     entities.CommandSources,
		Criteria: entities.CommandCriteria{Topic: "37signals"},
	}
	reconcileCriteria(&parsed)
	same(t, "from_topic", parsed.Criteria.FromTopic, "37signals")
	same(t, "topic", parsed.Criteria.Topic, "")
	if len(parsed.Notes) == 0 {
		t.Fatal("the translation was not recorded")
	}
	if !parsed.Criteria.NarrowsSources() {
		t.Fatal("a provenance topic does not narrow a source list")
	}
}

// The invariant behind the 922-row answer: a reading whose fields address only claims is NOT a
// narrowed source list, and must be refused rather than answered with the whole vault.
func TestCriteriaNarrowingIsLensAware(t *testing.T) {
	claimsOnly := entities.CommandCriteria{Topic: "caching", Order: entities.CommandOrderNewest}
	if claimsOnly.NarrowsSources() {
		t.Fatal("a claim topic must not narrow a source list")
	}
	if !claimsOnly.NarrowsClaims() {
		t.Fatal("a claim topic narrows a claim list")
	}
	if (entities.CommandCriteria{Order: entities.CommandOrderNewest}).NarrowsSources() {
		t.Fatal("an order is not a filter")
	}
	if !(entities.CommandCriteria{FromTopic: "caching"}).NarrowsSources() {
		t.Fatal("a provenance topic narrows a source list")
	}
}

// An utterance that names nothing is NOT "the whole vault": it is a request the app could not read.
func TestParseCommandRefusesToGuess(t *testing.T) {
	for _, text := range []string{"", "hmm", "mach das mal", "ok"} {
		if _, err := ParseCommand(text, CommandRefs{}); err == nil {
			t.Fatalf("%q was read as a command", text)
		}
	}
}

// A reader may POINT at what they are looking at, or paste an id. Both must narrow the set — that
// is what makes "the sources behind this claim" a command at all.
func TestParseCommandReferences(t *testing.T) {
	atom := ulid.Make().String()
	hash := strings.Repeat("ab", 32)

	cases := []struct {
		text  string
		refs  CommandRefs
		lens  entities.CommandLens
		check func(t *testing.T, c entities.CommandCriteria)
	}{
		{
			text: "zeige mir die quellen zu diesem claim",
			refs: CommandRefs{View: "claims", ID: atom},
			lens: entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "atom", c.Atom, atom)
				same(t, "text", c.Text, "")
			},
		},
		{
			text: "die claims aus dieser quelle",
			refs: CommandRefs{View: "source", ID: hash},
			lens: entities.CommandClaims,
			check: func(t *testing.T, c entities.CommandCriteria) {
				same(t, "source", c.Source, hash)
				same(t, "text", c.Text, "")
			},
		},
		{
			text:  "zeige mir " + hash,
			lens:  entities.CommandSources,
			check: func(t *testing.T, c entities.CommandCriteria) { same(t, "source", c.Source, hash) },
		},
		{
			text: "was sind die claims von " + atom,
			lens: entities.CommandAsk,
			check: func(t *testing.T, c entities.CommandCriteria) {
				// A question stays a question; the reference is still read.
				same(t, "atom", c.Atom, atom)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			parsed, err := ParseCommand(tc.text, tc.refs)
			if err != nil {
				t.Fatalf("ParseCommand: %v", err)
			}
			same(t, "lens", string(parsed.Lens), string(tc.lens))
			tc.check(t, parsed.Criteria)
		})
	}
}

// The wiki is a LIST, not a destination: "my pages about X" must materialize the matching pages,
// and a criterion attached to the vault report must be refused (it is not a filtered list).
func TestWikiAndStatusNarrowing(t *testing.T) {
	if !(entities.CommandCriteria{Text: "github"}).NarrowsWiki() {
		t.Fatal("text must narrow the wiki")
	}
	if !(entities.CommandCriteria{Tag: "rails"}).NarrowsWiki() {
		t.Fatal("a tag must narrow the wiki")
	}
	if (entities.CommandCriteria{Kind: "pdf"}).NarrowsWiki() {
		t.Fatal("a source kind must not narrow the wiki")
	}
	if !(entities.CommandCriteria{Kind: "pdf"}).NarrowsSources() {
		t.Fatal("a source kind narrows a source list")
	}

	// The reading is translated for the wiki too, and the translation is recorded. A wiki page has
	// its own edit time, so the window is NOT one of the removed criteria.
	parsed := ParsedCommand{Lens: entities.CommandWiki, Criteria: entities.CommandCriteria{
		Text: "github", Kind: "pdf", Window: entities.CommandWindow{Spec: "last-week"},
	}}
	reconcileCriteria(&parsed)
	same(t, "text", parsed.Criteria.Text, "github")
	same(t, "kind", parsed.Criteria.Kind, "")
	same(t, "window", parsed.Criteria.Window.Spec, "last-week")
	if len(parsed.Notes) == 0 {
		t.Fatal("the removed criteria were not named")
	}
}

// A reference word names the OBJECT of the request: "the sources of this claim" is a source list.
// If nothing is open, the request is refused with a reason instead of being read into the wrong
// lens (the word "claim" used to win and turn it into a claim list).
func TestParseCommandPendingReference(t *testing.T) {
	parsed, err := ParseCommand("die quellen dieses claims", CommandRefs{})
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	same(t, "lens", string(parsed.Lens), string(entities.CommandSources))
	same(t, "pending", parsed.PendingRef, "claim")
	same(t, "text", parsed.Criteria.Text, "")
}

// The URL is built by the server, never by the model: free text stays out of the address, and the
// record's id is always in it so a list can recognise itself as the assistant's.
func TestCommandURL(t *testing.T) {
	id := mustULID(t)
	base := entities.Command{ID: id, Lens: entities.CommandSources}

	filtered := base
	filtered.Criteria = entities.CommandCriteria{Tag: "cloud", Kind: "pdf"}
	got := CommandURL(filtered)
	want := "#/sources?command=" + id.String() + "&kind=pdf&tag=cloud"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}

	searched := base
	searched.Criteria = entities.CommandCriteria{Text: "cloud computing"}
	if got := CommandURL(searched); got != "#/sources?command="+id.String() {
		t.Fatalf("free text leaked into the address: %q", got)
	}

	claims := base
	claims.Lens = entities.CommandClaims
	claims.Criteria = entities.CommandCriteria{Topic: "caching"}
	if got := CommandURL(claims); got != "#/claims?command="+id.String()+"&topic=caching" {
		t.Fatalf("claims url = %q", got)
	}

	ask := base
	ask.Lens = entities.CommandAsk
	if got := CommandURL(ask); got != "#/ask" {
		t.Fatalf("ask url = %q", got)
	}

	wiki := base
	wiki.Lens = entities.CommandWiki
	if got := CommandURL(wiki); got != "#/wiki?command="+id.String() {
		t.Fatalf("wiki url = %q", got)
	}
}

// The step state is read from the traffic lights: "open" is neither finished nor failed.
func TestSourceStateMatches(t *testing.T) {
	open := entities.Source{Steps: entities.StepSet{
		entities.StepCapture: {State: entities.StepOK},
		entities.StepOutline: {State: entities.StepOK},
		entities.StepDistill: {State: entities.StepPending},
	}}
	failed := entities.Source{Steps: entities.StepSet{
		entities.StepCapture: {State: entities.StepOK},
		entities.StepOutline: {State: entities.StepFailed},
		entities.StepDistill: {State: entities.StepPending},
	}}
	done := entities.Source{Steps: entities.StepSet{
		entities.StepCapture: {State: entities.StepOK},
		entities.StepOutline: {State: entities.StepOK},
		entities.StepDistill: {State: entities.StepOK},
	}}
	bookmark := entities.Source{Steps: entities.StepSet{
		entities.StepCapture: {State: entities.StepOK},
		entities.StepOutline: {State: entities.StepSkipped},
		entities.StepDistill: {State: entities.StepSkipped},
	}}

	cases := []struct {
		name string
		src  entities.Source
		want entities.CommandState
		ok   bool
	}{
		{"open", open, entities.CommandStateOpen, true},
		{"open-is-not-failed", failed, entities.CommandStateOpen, false},
		{"failed", failed, entities.CommandStateFailed, true},
		{"done", done, entities.CommandStateDone, true},
		{"bookmark-is-done", bookmark, entities.CommandStateDone, true},
		{"open-is-not-done", open, entities.CommandStateDone, false},
		{"any", failed, entities.CommandStateAny, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceStateMatches(tc.src, tc.want); got != tc.ok {
				t.Fatalf("sourceStateMatches = %v, want %v", got, tc.ok)
			}
		})
	}
}

func same(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

func mustULID(t *testing.T) ulid.ULID {
	t.Helper()
	return ulid.Make()
}
