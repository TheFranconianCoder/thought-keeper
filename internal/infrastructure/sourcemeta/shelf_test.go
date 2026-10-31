package sourcemeta

import (
	"context"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

type shelfIndex struct {
	favoriteAt time.Time
	readAt     time.Time
	doneAt     time.Time
	manual     bool
	via        []string
}

func (i *shelfIndex) SetSourceSteps(context.Context, string, entities.StepSet) error { return nil }
func (i *shelfIndex) SetSourceTitle(context.Context, string, string) error           { return nil }
func (i *shelfIndex) SetSourceLanguage(context.Context, string, string) error        { return nil }
func (i *shelfIndex) SetSourceOrigin(context.Context, string, string, string, string) error {
	return nil
}
func (i *shelfIndex) ClearSourceOrigin(context.Context, string) error       { return nil }
func (i *shelfIndex) SetSourceTags(context.Context, string, []string) error { return nil }
func (i *shelfIndex) SetSourceAutoTags(context.Context, string, []string, time.Time) error {
	return nil
}
func (i *shelfIndex) SetSourceFavorite(_ context.Context, _ string, at time.Time) error {
	i.favoriteAt = at
	return nil
}
func (i *shelfIndex) SetSourceReadLater(_ context.Context, _ string, at, doneAt time.Time, manual bool, via []string) error {
	i.readAt, i.doneAt, i.manual, i.via = at, doneAt, manual, via
	return nil
}

// TestShelfLivesInTheRecord: the star and the queue are human state, so they live in the <hash>.meta
// file (the truth) and are mirrored into the index. A finished entry is never re-opened by a match.
func TestShelfLivesInTheRecord(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	meta := New(root)
	index := &shelfIndex{}
	tracker := NewTracker(meta, index, nil)
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.UnixMilli(1_700_000_000_000).UTC()

	if err := tracker.SetFavorite(ctx, hash, now); err != nil {
		t.Fatal(err)
	}
	rec, err := meta.Read(hash)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FavoriteAt != now.UnixMilli() || index.favoriteAt.IsZero() {
		t.Fatalf("favorite: record=%d index=%v", rec.FavoriteAt, index.favoriteAt)
	}

	if err := tracker.SetReadLater(ctx, hash, now, time.Time{}, true, nil); err != nil {
		t.Fatal(err)
	}
	rec, _ = meta.Read(hash)
	if !rec.ReadLaterManual || rec.ReadLaterAt != now.UnixMilli() {
		t.Fatalf("manual queue = %+v", rec)
	}

	// A match adds its slug without re-opening anything; a second match is a no-op.
	changed, err := tracker.QueueFromInterests(ctx, hash, []string{"rust-async"}, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("queue from interests: changed=%v err=%v", changed, err)
	}
	rec, _ = meta.Read(hash)
	if len(rec.ReadLaterVia) != 1 || rec.ReadLaterVia[0] != "rust-async" {
		t.Fatalf("via = %v", rec.ReadLaterVia)
	}
	changed, _ = tracker.QueueFromInterests(ctx, hash, []string{"rust-async"}, now)
	if changed {
		t.Fatal("the same match changed the record again")
	}

	// Finished: a later match must not re-open it.
	if err := tracker.SetReadLater(ctx, hash, now, now.Add(time.Hour), rec.ReadLaterManual, rec.ReadLaterVia); err != nil {
		t.Fatal(err)
	}
	changed, _ = tracker.QueueFromInterests(ctx, hash, []string{"other"}, now.Add(2*time.Minute))
	if changed {
		t.Fatal("a finished queue entry was re-opened by a match")
	}
	rec, _ = meta.Read(hash)
	if rec.ReadLaterDoneAt == 0 {
		t.Fatal("the done mark was lost")
	}
}
