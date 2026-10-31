package interests

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestInterestFilesRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := New(t.TempDir())
	created := time.UnixMilli(1_700_000_000_000).UTC()
	rule := interestFixture(created)
	if err := store.Save(ctx, rule); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(store.Path(rule.Slug))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	for _, want := range []string{"name: Rust async", "rust", "async", "exclude", "korrosion"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("file does not contain %q:\n%s", want, raw)
		}
	}

	got, err := store.Get(ctx, rule.Slug)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != rule.Name || len(got.Terms) != len(rule.Terms) || !got.Enabled {
		t.Fatalf("round trip = %+v, want %+v", got, rule)
	}
	if got.Created.UnixMilli() != created.UnixMilli() {
		t.Fatalf("created = %v, want %v", got.Created, created)
	}

	rules, err := store.List(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatalf("list = %v (%v), want one rule", rules, err)
	}
}

func TestAPausedRuleStaysPaused(t *testing.T) {
	ctx := context.Background()
	store := New(t.TempDir())
	rule := interestFixture(time.UnixMilli(1_700_000_000_000).UTC())
	rule.Enabled = false
	if err := store.Save(ctx, rule); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, rule.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatal("a paused rule came back enabled")
	}
}

func TestRejectionIsAReadModifyWrite(t *testing.T) {
	ctx := context.Background()
	store := New(t.TempDir())
	rule := interestFixture(time.UnixMilli(1_700_000_000_000).UTC())
	if err := store.Save(ctx, rule); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("ab", 32)
	got, err := store.SetRejected(ctx, rule.Slug, hash, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rejected) != 1 || got.Rejected[0] != hash {
		t.Fatalf("rejected = %v", got.Rejected)
	}
	got, err = store.SetRejected(ctx, rule.Slug, hash, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rejected) != 0 {
		t.Fatalf("rejected after clearing = %v", got.Rejected)
	}
}

func TestMissingDirectoryIsAnEmptyVault(t *testing.T) {
	ctx := context.Background()
	store := New(t.TempDir() + "/never-created")
	rules, err := store.List(ctx)
	if err != nil || len(rules) != 0 {
		t.Fatalf("list = %v (%v), want an empty list", rules, err)
	}
}

func interestFixture(created time.Time) entities.Interest {
	return entities.Interest{
		Slug: "rust-async", Name: "Rust async",
		Terms: []string{"rust", "async"}, Exclude: []string{"korrosion"},
		Kinds: []string{"pdf"}, Enabled: true, Created: created, Updated: created,
	}
}
