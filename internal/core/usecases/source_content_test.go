package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// TestContentReportsAMissingSnapshotAsNotFound: a source whose bytes never arrived (a link whose
// fetch failed, a file moved out of the store) has no snapshot. The reader is told that plainly;
// the filesystem's "no such file or directory" and the path behind it stay out of the answer.
func TestContentReportsAMissingSnapshotAsNotFound(t *testing.T) {
	hash := strings.Repeat("a", 64)
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{Hash: hash, Kind: entities.SourceKindURL, Ext: ".md", Title: "a page"}
	uc := usecases.Sources{Store: objects.New(t.TempDir()), Index: idx}

	_, _, _, err := uc.Content(context.Background(), hash)
	if err == nil {
		t.Fatal("a source without bytes must report an error")
	}
	if code := types.CodeOf(err); code != "not_found" {
		t.Fatalf("code = %q, want not_found", code)
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Fatalf("the filesystem sentence leaked: %v", err)
	}
}
