package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

type fakeReaderStore struct {
	seen   time.Time
	exists bool
	writes []time.Time
}

func (f *fakeReaderStore) Read(context.Context) (time.Time, bool, error) {
	return f.seen, f.exists, nil
}

func (f *fakeReaderStore) Write(_ context.Context, at time.Time) error {
	f.seen, f.exists = at, true
	f.writes = append(f.writes, at)
	return nil
}

type fakeReaderIndex struct{ newest time.Time }

func (f fakeReaderIndex) MaxSourceImportedAt(context.Context) (time.Time, error) {
	return f.newest, nil
}

type fakeReaderClock struct{ now time.Time }

func (f fakeReaderClock) Now() time.Time { return f.now }

func at(hour int) time.Time {
	return time.Date(2026, 11, 7, hour, 0, 0, 0, time.UTC)
}

// TestFirstActivationInitializesWithoutMarkingTheWholeVault: a fresh vault shows no "new" sources,
// and the record starts at the activation instant so the next arrival is new.
func TestFirstActivationInitializesWithoutMarkingTheWholeVault(t *testing.T) {
	store := &fakeReaderStore{}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(8)}}

	act, err := uc.Activate(context.Background())
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.First || !act.Changed {
		t.Fatalf("first = %v, changed = %v, want true/true", act.First, act.Changed)
	}
	if !act.Since.IsZero() {
		t.Fatalf("since = %v, want zero: the first open marks nothing", act.Since)
	}
	if !act.SeenAt.Equal(at(8)) {
		t.Fatalf("seen_at = %v, want the activation instant", act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].Equal(at(8)) {
		t.Fatalf("writes = %v, want the activation instant", store.writes)
	}
}

// TestActivationMovesToTheNewestArrival: the baseline is the previous watermark and the new one is
// the newest source, so the client marks exactly the batch it just acknowledged.
func TestActivationMovesToTheNewestArrival(t *testing.T) {
	store := &fakeReaderStore{seen: at(6), exists: true}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background())
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.First || !act.Changed {
		t.Fatalf("first = %v, changed = %v, want false/true", act.First, act.Changed)
	}
	if !act.Since.Equal(at(6)) || !act.SeenAt.Equal(at(9)) {
		t.Fatalf("since/seen = %v/%v, want 6/9", act.Since, act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].Equal(at(9)) {
		t.Fatalf("writes = %v, want the newest arrival", store.writes)
	}
}

// TestActivationWithoutANewArrivalChangesNothing: the watermark never moves backwards and an
// activation with nothing new writes and announces nothing.
func TestActivationWithoutANewArrivalChangesNothing(t *testing.T) {
	store := &fakeReaderStore{seen: at(9), exists: true}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background())
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.Changed || act.First {
		t.Fatalf("changed = %v, first = %v, want false/false", act.Changed, act.First)
	}
	if !act.Since.Equal(at(9)) || !act.SeenAt.Equal(at(9)) {
		t.Fatalf("since/seen = %v/%v, want 9/9", act.Since, act.SeenAt)
	}
	if len(store.writes) != 0 {
		t.Fatalf("writes = %v, want none", store.writes)
	}
}

// TestStateReadsWithoutAcknowledging keeps a resync a pure read.
func TestStateReadsWithoutAcknowledging(t *testing.T) {
	store := &fakeReaderStore{seen: at(6), exists: true}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	st, err := uc.State(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !st.SeenAt.Equal(at(6)) {
		t.Fatalf("seen = %v, want 6", st.SeenAt)
	}
	if len(store.writes) != 0 {
		t.Fatalf("state wrote: %v", store.writes)
	}
}
