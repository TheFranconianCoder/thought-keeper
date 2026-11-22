package usecases_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// digestAtoms is the observed claim catalog `list_topics` reads. Only ListClaimTopics is reached, so the
// embedded nil interface is honest.
type digestAtoms struct {
	interfaces.AtomRepository
	topics []entities.TopicUse
}

func (a *digestAtoms) ListClaimTopics(context.Context) ([]entities.TopicUse, error) {
	return a.topics, nil
}

// digestVocab is the vocabulary record of a test: a shelf and nothing else.
type digestVocab struct{ shelf []string }

func (v digestVocab) Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error) {
	return entities.Vocabulary{Namespace: ns, Shelf: v.shelf}, nil
}

func (v digestVocab) Write(entities.Vocabulary) error { return nil }

// digestRow is one entry of the tool's JSON answer.
type digestRow struct {
	Topic           string `json:"topic"`
	Claims          int    `json:"claims"`
	MatchesQuestion bool   `json:"matches_question"`
}

// askListTopics runs one question through the loop, answers the model's `list_topics` call, and decodes
// the tool result the model saw.
func askListTopics(t *testing.T, uc usecases.Query, question string) []digestRow {
	t.Helper()
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_topics", `{}`),
		finalResponse("The vault files claims under these subjects."),
	}}
	uc.Gateway = gw
	uc.Runs = &fakeRunRepo{}
	uc.Model = "test"
	uc.MaxIterations = 4
	uc.Clock = fixedClock{}

	if _, err := uc.Ask(context.Background(), ulid.Make(), question); err != nil {
		t.Fatalf("query: %v", err)
	}
	var rows []digestRow
	if err := json.Unmarshal([]byte(toolMessage(gw.requests[1])), &rows); err != nil {
		t.Fatalf("list_topics answer is not a JSON array: %v (%q)", err, toolMessage(gw.requests[1]))
	}
	return rows
}

// TestListTopicsReadsTheShelf: the orientation is the shelf the claims may be filed under, not the live
// catalog. A shelf name that carries few claims is visible, a name the shelf does not carry is not, and
// the share band keeps the catch-all and the thin name out — even when the shelf itself names them.
func TestListTopicsReadsTheShelf(t *testing.T) {
	uc := usecases.Query{
		Atoms: &digestAtoms{topics: []entities.TopicUse{
			{Topic: "misc", Count: 90000},
			{Topic: "a-way-of-life-speech", Count: 12000},
			{Topic: "off-shelf-name", Count: 1500},
			{Topic: "caching", Count: 900},
			{Topic: "software-architecture", Count: 300},
			{Topic: "databases", Count: 200},
			{Topic: "a-b-testing", Count: 100},
			{Topic: "rare-topic", Count: 2},
		}},
		Vocab: digestVocab{shelf: []string{
			"a-way-of-life-speech", "caching", "software-architecture", "databases", "a-b-testing", "rare-topic",
		}},
		TopicShelf: usecases.ShelfLimits{MaxShare: 0.02, MinShare: 0.0001},
	}

	rows := askListTopics(t, uc, "How do databases work?")

	// The names the question resolves onto come first, ahead of names that carry more claims.
	if len(rows) == 0 {
		t.Fatal("list_topics answered nothing")
	}
	if rows[0].Topic != "databases" || !rows[0].MatchesQuestion {
		t.Errorf("first entry = %+v, want databases marked as the question's name", rows[0])
	}
	if len(rows) < 4 || rows[1].Topic != "caching" || rows[2].Topic != "software-architecture" || rows[3].Topic != "a-b-testing" {
		t.Errorf("rows = %+v, want the shelf most used first after the match", rows)
	}
	for _, row := range rows {
		switch row.Topic {
		case "a-way-of-life-speech":
			t.Error("the digest offered a catch-all the band refused")
		case "rare-topic":
			t.Error("the digest offered a thin name")
		case "off-shelf-name":
			t.Error("the digest offered a name the shelf does not carry")
		case "misc":
			t.Error("the digest offered an off-shelf name")
		}
	}
	if rows[1].MatchesQuestion || rows[3].Claims != 100 {
		t.Errorf("rows = %+v, want the claim counts and no match mark on the others", rows)
	}
}

// TestListTopicsWithoutAShelfUsesTheCatalog: a vault below its target has no shelf record — its catalog
// IS the shelf. The digest is then the most used names, judged by no band, never the alphabetical head.
func TestListTopicsWithoutAShelfUsesTheCatalog(t *testing.T) {
	uc := usecases.Query{
		Atoms: &digestAtoms{topics: []entities.TopicUse{
			{Topic: "1-2-3-counting", Count: 3},
			{Topic: "abc-notation", Count: 2},
			{Topic: "caching", Count: 900},
			{Topic: "zfs", Count: 40},
		}},
		// No Vocab and no band: the catalog answers as it is.
	}

	rows := askListTopics(t, uc, "What does caching cost?")

	if len(rows) != 4 {
		t.Fatalf("rows = %+v, want every name of the catalog", rows)
	}
	if rows[0].Topic != "caching" || !rows[0].MatchesQuestion {
		t.Errorf("first entry = %+v, want caching marked as the question's name", rows[0])
	}
	if rows[1].Topic != "zfs" {
		t.Errorf("second entry = %+v, want zfs (40 claims) ahead of the three-claim names", rows[1])
	}
}

// TestListTopicsStaysUnderTheToolBudget: a shelf of long slugs must not hand the agent loop an array it
// would cut mid-JSON. The digest stops at its byte budget instead of trusting the row count.
func TestListTopicsStaysUnderTheToolBudget(t *testing.T) {
	topics := make([]entities.TopicUse, 0, 120)
	shelf := make([]string, 0, 120)
	for i := range 120 {
		name := fmt.Sprintf("%02d-%s", i, strings.Repeat("long-subject-name-", 4))
		topics = append(topics, entities.TopicUse{Topic: name, Count: 1000 - i})
		shelf = append(shelf, name)
	}
	uc := usecases.Query{
		Atoms:      &digestAtoms{topics: topics},
		Vocab:      digestVocab{shelf: shelf},
		TopicShelf: usecases.ShelfLimits{MaxShare: 0.5, MinShare: 0},
	}

	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		toolResponse("list_topics", `{}`),
		finalResponse("The vault files claims under long subject names."),
	}}
	uc.Gateway = gw
	uc.Runs = &fakeRunRepo{}
	uc.Model = "test"
	uc.MaxIterations = 4
	uc.Clock = fixedClock{}
	if _, err := uc.Ask(context.Background(), ulid.Make(), "long subject name"); err != nil {
		t.Fatalf("query: %v", err)
	}

	answer := toolMessage(gw.requests[1])
	if len(answer) > 6000 {
		t.Fatalf("list_topics answered %d bytes, want under the loop's 6 000-byte cut", len(answer))
	}
	var rows []digestRow
	if err := json.Unmarshal([]byte(answer), &rows); err != nil {
		t.Fatalf("the answer was cut mid-JSON: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("list_topics answered nothing")
	}
}
