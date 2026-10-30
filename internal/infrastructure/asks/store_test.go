package asks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestStoreWritesReadsListsAndTrashes(t *testing.T) {
	dir := t.TempDir()
	s := New(dir + "/asks")
	now := time.Now().UTC().Truncate(time.Second)
	a := entities.Ask{ID: ulid.Make(), Question: "What is caching?", Answer: "Caching stores a result.", Source: "7f3a1c0000000000000000000000000000000000000000000000000000000000", CreatedAt: now, UpdatedAt: now}
	if err := s.Write(context.Background(), a); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.Get(context.Background(), a.ID)
	if err != nil || got.Question != a.Question || got.Answer != a.Answer || got.Source != a.Source {
		t.Fatalf("get = %+v, %v", got, err)
	}
	list, err := s.List(context.Background(), 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.Delete(context.Background(), a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(context.Background(), a.ID); err == nil {
		t.Fatal("deleted ask is still readable")
	}
}

func TestStoreNormalizesLegacyCitations(t *testing.T) {
	dir := t.TempDir()
	s := New(dir + "/asks")
	now := time.Now().UTC().Truncate(time.Second)
	a := entities.Ask{ID: ulid.Make(), Question: "q", Answer: "Prose [[claim:01J8ZC5V9P0R7T2QK4W6Y8B3ND|Claim text]].", CreatedAt: now, UpdatedAt: now}
	if err := s.Write(context.Background(), a); err != nil {
		t.Fatalf("write: %v", err)
	}
	n, err := s.Normalize(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("normalize = %d, %v", n, err)
	}
	got, err := s.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(got.Answer, "[[claim:") || !strings.Contains(got.Answer, "](#/claims/") {
		t.Fatalf("answer was not normalised: %q", got.Answer)
	}
}
