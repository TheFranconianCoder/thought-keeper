package sqlc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestGetSourceByHashProjectsTheAttachment: the source page needs to know that its snapshot was
// attached by hand, so the wire projection is readable from the index without parsing meta in the UI.
// An explicit null in a later meta write clears it — which is how a successful refetch regularizes the
// source.
func TestGetSourceByHashProjectsTheAttachment(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSourceRepository(db)
	hash := strings.Repeat("a7", 32)
	if err := repo.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/a7/a7/" + hash + ".md", URL: "https://example.com/member",
		Title: "Member", TreeState: entities.TreeNone, ImportedAt: time.UnixMilli(1_700_000_000_000).UTC(),
		Meta: `{"url":"https://example.com/member","attached":true,"attached_from":"page.html"}`,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repo.GetSourceByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Attached || got.AttachedFrom != "page.html" {
		t.Fatalf("attachment = %v/%q, want true/page.html", got.Attached, got.AttachedFrom)
	}

	if err := repo.SetSourceMeta(ctx, hash, `{"url":"https://example.com/member","attached":null,"attached_from":null}`); err != nil {
		t.Fatalf("clear meta: %v", err)
	}
	if got, err = repo.GetSourceByHash(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if got.Attached || got.AttachedFrom != "" {
		t.Errorf("the attachment survived an explicit null: %+v", got)
	}
}

// TestSetSourceMetaKeepsOwnership is the regression behind "this source was derived from a wiki page,
// but the source page shows no wiki page". Ownership lives ONLY in the row's meta JSON, and writers
// that rebuild that JSON from the snapshot's own fields (a refresh, a reindex) dropped it — silently,
// because the record (<hash>.meta) still had it. The repository keeps it now: a meta write may say
// what the snapshot is, not who owns the source.
func TestSetSourceMetaKeepsOwnership(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSourceRepository(db)
	hash := strings.Repeat("c1", 32)
	const pageID = "01M5JAHSRBDSX83F8V6CR3DRH3"
	now := time.UnixMilli(1_700_000_000_000).UTC()

	if err := repo.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/c1/c1/" + hash + ".md", URL: "https://example.com/linked",
		Title: "Linked", TreeState: entities.TreeNone, ImportedAt: now,
		Origin: entities.WikiLinkOrigin, WikiPage: pageID, Meta: `{"url":"https://example.com/linked"}`,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The snapshot write: content hash and icon, nothing about the owner.
	if err := repo.SetSourceMeta(ctx, hash, `{"url":"https://example.com/linked","content_hash":"abc"}`); err != nil {
		t.Fatalf("set meta: %v", err)
	}
	got, err := repo.GetSourceByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != entities.WikiLinkOrigin || got.WikiPage != pageID {
		t.Errorf("ownership = %q/%q, want the wiki page kept", got.Origin, got.WikiPage)
	}
	if !strings.Contains(got.Meta, `"content_hash":"abc"`) {
		t.Errorf("the snapshot fields were not written: %s", got.Meta)
	}

	// A caller that NAMES the key is making a decision — an explicit null clears it.
	if err := repo.SetSourceMeta(ctx, hash, `{"url":"https://example.com/linked","origin":null}`); err != nil {
		t.Fatalf("set meta 2: %v", err)
	}
	if got, err = repo.GetSourceByHash(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if got.Origin != "" {
		t.Errorf("an explicit null did not clear the owner: %q", got.Origin)
	}
}
