package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// TestResetClearsPartRecordsWithoutClaims: a source that correctly produced no claim (a link list,
// document metadata) still has a done part and a summary. A reset must clear that bookkeeping even
// though there is no atom to delete: "no atoms" is not "nothing to clear". Leaving the done part
// behind made the next run skip its only part while the pipeline kept asking for a derivation that
// had already happened.
func TestResetClearsPartRecordsWithoutClaims(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("ef", 32)
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Title: "Links", Kind: entities.SourceKindMD, Ext: ".md",
	}
	parts := &fakeSections{m: map[string]entities.PartState{
		"0.1": {NodeID: "0.1", State: entities.PartDone, Reason: "source_relative"},
	}}

	clean := usecases.CleanSource{
		Atoms: &fakeAtomRepo{}, AtomStore: &fakeAtomStore{m: map[string]entities.Atom{}},
		Parts: parts, Index: idx, Clock: fixedClock{},
	}
	if _, err := clean.Execute(ctx, hash); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if len(parts.m) != 0 {
		t.Fatalf("a reset with no claim left the part records behind: %+v", parts.m)
	}
}
