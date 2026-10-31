package sourcemeta_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
)

// tagIndex is the narrow index mirror the tracker writes through. Only the tag method matters here;
// the others exist to satisfy the interface.
type tagIndex struct {
	tags   map[string][]string
	auto   map[string][]string
	autoAt time.Time
}

func (i *tagIndex) SetSourceSteps(context.Context, string, entities.StepSet) error { return nil }

func (i *tagIndex) SetSourceFavorite(context.Context, string, time.Time) error { return nil }

func (i *tagIndex) SetSourceReadLater(context.Context, string, time.Time, time.Time, bool, []string) error {
	return nil
}
func (i *tagIndex) SetSourceTitle(context.Context, string, string) error    { return nil }
func (i *tagIndex) SetSourceLanguage(context.Context, string, string) error { return nil }
func (i *tagIndex) SetSourceOrigin(context.Context, string, string, string, string) error {
	return nil
}
func (i *tagIndex) ClearSourceOrigin(context.Context, string) error { return nil }

func (i *tagIndex) SetSourceTags(_ context.Context, hash string, tags []string) error {
	if i.tags == nil {
		i.tags = map[string][]string{}
	}
	i.tags[hash] = append([]string(nil), tags...)
	return nil
}

func (i *tagIndex) SetSourceAutoTags(_ context.Context, hash string, tags []string, at time.Time) error {
	if i.auto == nil {
		i.auto = map[string][]string{}
	}
	i.auto[hash] = append([]string(nil), tags...)
	i.autoAt = at
	return nil
}

// TestSourceTagsLiveInTheRecordAndTheIndex: the .meta file is the truth (a reindex reads it back),
// the index mirrors it (the list and the vocabulary read it without opening files).
func TestSourceTagsLiveInTheRecordAndTheIndex(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	meta := sourcemeta.New(root)
	index := &tagIndex{}
	tracker := sourcemeta.NewTracker(meta, index, nil)
	hash := strings.Repeat("a1", 32)

	if err := tracker.SetSourceTags(ctx, hash, []string{"go", "caching"}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	rec, err := meta.Read(hash)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if len(rec.Tags) != 2 || rec.Tags[0] != "go" || rec.Tags[1] != "caching" {
		t.Fatalf("record tags = %v, want the labels", rec.Tags)
	}
	if len(index.tags[hash]) != 2 {
		t.Fatalf("index tags = %v, want the mirror", index.tags[hash])
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()
	if err := tracker.SetSourceAutoTags(ctx, hash, []string{"caching", "reference"}, at); err != nil {
		t.Fatalf("set auto tags: %v", err)
	}
	rec, err = meta.Read(hash)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if len(rec.AutoTags) != 2 || rec.AutoTagsAt != at.UnixMilli() {
		t.Fatalf("record auto tags = %v at %d, want the labels and the marker", rec.AutoTags, rec.AutoTagsAt)
	}
	if len(index.auto[hash]) != 2 || index.autoAt.IsZero() {
		t.Fatalf("index auto tags = %v, want the mirror", index.auto[hash])
	}

	all, err := tracker.SourceTags(ctx)
	if err != nil {
		t.Fatalf("source tags: %v", err)
	}
	if len(all[hash].Tags) != 2 || all[hash].Tags[1] != "caching" {
		t.Fatalf("SourceTags = %v, want the record's labels", all)
	}
	if len(all[hash].AutoTags) != 2 || all[hash].AutoTagsAt.IsZero() {
		t.Fatalf("SourceTags auto = %v, want the derived labels", all[hash])
	}
}
