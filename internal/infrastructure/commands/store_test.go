package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/commands"
)

func fixture() entities.Command {
	return entities.Command{
		ID:         ulid.Make(),
		Text:       "zeige mir alle quellen zum thema cloud",
		Lens:       entities.CommandSources,
		URL:        "#/sources?command=x&tag=cloud",
		Generated:  "rules",
		Resolution: "filter",
		Rationale:  "2 sources carry the tag cloud",
		Notes:      []string{"the topic was read as provenance"},
		Total:      2,
		Vault:      40,
		Scanned:    40,
		CreatedAt:  time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
		Criteria: entities.CommandCriteria{
			Tag: "cloud", FromTopic: "cloud", Kind: "pdf", State: entities.CommandStateOpen,
			Since: 7 * 24 * time.Hour, Order: entities.CommandOrderNewest,
		},
		Items: []entities.CommandItem{
			{Ref: strings.Repeat("a", 64), Title: "Cloud Architecture", Kind: "pdf"},
			{Ref: strings.Repeat("b", 64), Title: "Cloud Security", Kind: "pdf"},
		},
	}
}

// A command is a record: what was asked, what it meant, and the set it produced — in that order.
func TestStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	store := commands.New(root)
	want := fixture()
	if err := store.Write(context.Background(), want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := store.Get(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Text != want.Text || got.Lens != want.Lens || got.URL != want.URL {
		t.Fatalf("record = %+v", got)
	}
	if got.Criteria.Tag != "cloud" || got.Criteria.Kind != "pdf" || got.Criteria.Since != want.Criteria.Since {
		t.Fatalf("criteria = %+v", got.Criteria)
	}
	if got.Criteria.FromTopic != "cloud" {
		t.Fatalf("from_topic did not survive: %+v", got.Criteria)
	}
	if got.Resolution != "filter" || got.Rationale == "" || len(got.Notes) != 1 {
		t.Fatalf("the reading did not survive: resolution=%q rationale=%q notes=%v",
			got.Resolution, got.Rationale, got.Notes)
	}
	if got.Total != 2 || got.Vault != 40 || len(got.Items) != 2 || got.Items[0].Ref != want.Items[0].Ref {
		t.Fatalf("set = %+v", got)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("created = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

// The history is newest first, and one malformed file must not hide the rest.
func TestStoreListSkipsBrokenFiles(t *testing.T) {
	root := t.TempDir()
	store := commands.New(root)

	older := fixture()
	older.ID = ulid.Make()
	older.CreatedAt = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	newer := fixture()
	newer.ID = ulid.Make()
	newer.CreatedAt = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

	for _, cmd := range []entities.Command{older, newer} {
		if err := store.Write(context.Background(), cmd); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ulid.Make().String()+".md"), []byte("not a record"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := store.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("list = %d records, want 2", len(items))
	}
	if items[0].ID != newer.ID {
		t.Fatal("the list is not newest first")
	}
}

// Expiry is a decision, not an accident: the record goes to the trash, the vault keeps the object.
func TestStorePrune(t *testing.T) {
	root := t.TempDir()
	store := commands.New(root)

	old := fixture()
	old.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
	fresh := fixture()
	fresh.ID = ulid.Make()
	fresh.CreatedAt = time.Now().Add(-time.Hour)
	for _, cmd := range []entities.Command{old, fresh} {
		if err := store.Write(context.Background(), cmd); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	removed, err := store.Prune(context.Background(), time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := store.Get(context.Background(), old.ID); err == nil {
		t.Fatal("the expired command is still readable")
	}
	if _, err := store.Get(context.Background(), fresh.ID); err != nil {
		t.Fatalf("the fresh command was pruned: %v", err)
	}
	trashed, err := os.ReadDir(filepath.Join(root, "..", ".trash", "commands"))
	if err != nil {
		t.Fatalf("trash: %v", err)
	}
	if len(trashed) != 1 {
		t.Fatalf("trash holds %d files, want 1", len(trashed))
	}
}
