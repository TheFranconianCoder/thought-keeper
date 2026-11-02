package usecases

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/commands"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
)

type commandGateway struct{ answer string }

func (g commandGateway) Complete(context.Context, interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	return interfaces.AgentResponse{
		Message: interfaces.AgentMessage{Role: "assistant", Content: g.answer},
	}, nil
}

// commandTestClock is the reader's clock for the window tests. It is 2023-11-14 22:13 UTC, which is
// 23:13 in Berlin: "yesterday" is 2023-11-13, and a source imported at 12:00 Berlin is in it.
type commandTestClock struct{}

func (commandTestClock) Now() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

func commandFixture(t *testing.T) (Commands, *sqlc.SourceRepository, *sqlc.AtomRepository, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sources := sqlc.NewSourceRepository(db)
	atoms := sqlc.NewAtomRepository(db)
	search := sqlc.NewSearchRepository(db, true)
	uc := Commands{
		Index:     sources,
		Atoms:     atoms,
		Lexical:   search,
		Store:     commands.New(t.TempDir()),
		Clock:     commandTestClock{},
		MaxItems:  100,
		ScanLimit: 1000,
	}
	return uc, sources, atoms, db
}

func seedCommandSource(t *testing.T, sources *sqlc.SourceRepository, hash, title, url string, imported time.Time) {
	t.Helper()
	if err := sources.UpsertSource(context.Background(), entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: hash + ".md", Title: title, URL: url, OriginalName: url,
		Bytes: 10, TreeState: entities.TreeBuilt, ImportedAt: imported, Meta: "{}",
	}); err != nil {
		t.Fatalf("source %s: %v", title, err)
	}
}

func seedCommandAtom(t *testing.T, atoms *sqlc.AtomRepository, claim, topic, hash string, created time.Time) {
	t.Helper()
	if err := atoms.SaveAtom(context.Background(), entities.Atom{
		ID: ulid.Make(), Topic: topic, Path: "test/" + topic + ".md",
		Claim: claim, Body: claim, Status: entities.AtomActive,
		Sources:   []entities.AtomSource{{Hash: hash, NodeID: "0.1"}},
		CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		t.Fatalf("atom %q: %v", claim, err)
	}
}

// A list question is a Sources filter, not Ask. The direct title/URL match wins; when the words
// live only in the claims, the strict mention channel maps the claim's provenance to the source.
func TestCommandListQuestionAndMention(t *testing.T) {
	uc, sources, atoms, db := commandFixture(t)
	ctx := context.Background()
	now := commandTestClock{}.Now()
	dhhHash := strings.Repeat("aa", 32)
	alistairHash := strings.Repeat("bb", 32)
	seedCommandSource(t, sources, dhhHash, "Europe is weak and delusional", "https://world.hey.com/dhh/europe", now.Add(-time.Hour))
	seedCommandSource(t, sources, alistairHash, "Hexagonal Architecture", "https://example.com/hexagonal", now.Add(-time.Hour))
	seedCommandAtom(t, atoms, "Alistair Cockburn originated the Hexagonal Architecture style.", "architecture", alistairHash, now)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}

	cmd, err := uc.Run(ctx, "Was haben wir von DHH?", CommandRefs{Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("DHH command: %v", err)
	}
	if cmd.Lens != string(entities.CommandSources) || cmd.Resolution != "filter" {
		t.Fatalf("DHH reading = lens %q resolution %q", cmd.Lens, cmd.Resolution)
	}
	if cmd.Criteria.Text != "DHH" || cmd.Total != 1 {
		t.Fatalf("DHH = text %q total %d, want DHH/1", cmd.Criteria.Text, cmd.Total)
	}

	cmd, err = uc.Run(ctx, "Quellen von Alistair Cockburn", CommandRefs{Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("Cockburn command: %v", err)
	}
	if cmd.Total != 1 || cmd.Resolution != "filter" {
		t.Fatalf("Cockburn = total %d resolution %q, want 1/filter", cmd.Total, cmd.Resolution)
	}
	foundMention := false
	for _, note := range cmd.Notes {
		if strings.Contains(note, "mention") {
			foundMention = true
		}
	}
	if !foundMention {
		t.Fatalf("the mention channel was not named: %v", cmd.Notes)
	}

	cmd, err = uc.Run(ctx, "Quellen von Sebastian Bauer", CommandRefs{Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("Bauer command: %v", err)
	}
	if cmd.Total != 0 {
		t.Fatalf("an absent name returned %d sources: %v", cmd.Total, cmd.Items)
	}
}

// An explicit tag the vault does not carry falls back to the vocabulary: "tagged cloud" may mean
// the tag "cloud-computing". A tag that exists but is excluded by another criterion does not.
func TestCommandUnknownTagFallsBackToVocabulary(t *testing.T) {
	uc, sources, _, _ := commandFixture(t)
	ctx := context.Background()
	hash := strings.Repeat("ee", 32)
	seedCommandSource(t, sources, hash, "Cloud architecture", "https://example.com/cloud", commandTestClock{}.Now())
	if err := sources.SetSourceTags(ctx, hash, []string{"cloud-computing"}); err != nil {
		t.Fatal(err)
	}
	cmd, err := uc.Run(ctx, "sources tagged cloud", CommandRefs{Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("tag command: %v", err)
	}
	if cmd.Total != 1 || cmd.Resolution != "mapped" {
		t.Fatalf("tag fallback = total %d resolution %q, want 1/mapped", cmd.Total, cmd.Resolution)
	}
	found := false
	for _, name := range cmd.Mapped {
		if name.Name == "cloud-computing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the mapped name was not recorded: %+v", cmd.Mapped)
	}
}

// A literal word + window that finds nothing falls back to the vocabulary: "llm" is the topic
// "large-language-models", and the window still applies. The model is what expands the word.
func TestCommandLiteralMissFallsBackToMapping(t *testing.T) {
	uc, sources, atoms, db := commandFixture(t)
	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("timezone not available: %v", err)
	}
	hash := strings.Repeat("ff", 32)
	yesterdayNoon := time.Date(2023, 11, 13, 12, 0, 0, 0, loc)
	seedCommandSource(t, sources, hash, "A post", "https://example.com/post", yesterdayNoon)
	seedCommandAtom(t, atoms, "Large language models change how teams write software.", "large-language-models", hash, yesterdayNoon)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}
	uc.Gateway = commandGateway{answer: `{"terms":["large-language-models","machine-learning"]}`}
	uc.Model = "test"

	cmd, err := uc.Run(ctx, "llm, added yesterday", CommandRefs{Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("llm command: %v", err)
	}
	if cmd.Total != 1 || cmd.Resolution != "mapped" {
		t.Fatalf("llm fallback = total %d resolution %q, want 1/mapped", cmd.Total, cmd.Resolution)
	}
	if cmd.Criteria.Window.Spec != "yesterday" {
		t.Fatalf("window = %q, want yesterday", cmd.Criteria.Window.Spec)
	}
	found := false
	for _, name := range cmd.Mapped {
		if name.Name == "large-language-models" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the mapped topic was not recorded: %+v", cmd.Mapped)
	}
}

// The calendar window is the reader's calendar, in SQL, and it includes only the named day.
func TestCommandCalendarWindow(t *testing.T) {
	uc, sources, _, _ := commandFixture(t)
	ctx := context.Background()
	now := commandTestClock{}.Now()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("timezone not available: %v", err)
	}
	yesterdayNoon := time.Date(2023, 11, 13, 12, 0, 0, 0, loc)
	oldHash := strings.Repeat("cc", 32)
	todayHash := strings.Repeat("dd", 32)
	seedCommandSource(t, sources, oldHash, "Yesterday's source", "https://example.com/yesterday", yesterdayNoon)
	seedCommandSource(t, sources, todayHash, "Today's source", "https://example.com/today", now)

	cmd, err := uc.Run(ctx, "Sources added yesterday", CommandRefs{Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("yesterday command: %v", err)
	}
	if cmd.Criteria.Window.Spec != "yesterday" || cmd.Total != 1 {
		t.Fatalf("yesterday = window %q total %d, want yesterday/1", cmd.Criteria.Window.Spec, cmd.Total)
	}
	if len(cmd.Items) != 1 || cmd.Items[0].Ref != oldHash {
		t.Fatalf("yesterday items = %+v, want the old source", cmd.Items)
	}
}
