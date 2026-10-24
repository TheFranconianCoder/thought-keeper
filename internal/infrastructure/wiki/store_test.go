package wiki

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestWikiPageRoundTripAndPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	s := New(root)
	id := ulid.Make()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	page := entities.WikiPage{
		ID: id, Title: "Go Concurrency", Tags: []string{"Go", "concurrency", "go"},
		URLs: []string{"https://example.com/a"},
		Body: "# Channels\n\nChannels connect goroutines.", SourceHash: entities.WikiHash(id),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Write(context.Background(), page); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.Read(context.Background(), id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Title != page.Title || got.Body != page.Body || got.SourceHash != page.SourceHash {
		t.Fatalf("round trip = %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "go" || got.Tags[1] != "concurrency" {
		t.Fatalf("tags = %v, want normalized unique tags", got.Tags)
	}
	if len(got.URLs) != 1 || got.URLs[0] != "https://example.com/a" {
		t.Fatalf("urls = %v, want the tracked URL", got.URLs)
	}
	list, err := s.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := s.Remove(context.Background(), id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := s.Read(context.Background(), id); err == nil {
		t.Fatal("removed page still readable")
	}
}

func TestListWikiPagesMissingDirectoryIsEmpty(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "not-there"))
	pages, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pages) != 0 {
		t.Fatalf("pages = %v, want none", pages)
	}
}
