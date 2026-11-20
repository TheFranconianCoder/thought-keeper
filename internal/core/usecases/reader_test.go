package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

type fakeReaderStore struct {
	state  entities.ReaderState
	writes []time.Time
}

func (f *fakeReaderStore) Read(context.Context) (entities.ReaderState, error) {
	return f.state, nil
}

func (f *fakeReaderStore) WriteSeen(_ context.Context, at time.Time) error {
	f.state.SourcesSeenAt = at
	f.writes = append(f.writes, at)
	return nil
}

type fakeReaderIndex struct{ newest time.Time }

func (f fakeReaderIndex) MaxSourceActivityAt(context.Context) (time.Time, error) {
	return f.newest, nil
}

type fakeReaderClock struct{ now time.Time }

func (f fakeReaderClock) Now() time.Time { return f.now }

func at(hour int) time.Time {
	return time.Date(2026, 11, 7, hour, 0, 0, 0, time.UTC)
}

// TestStartVisitSettlesNothing: the front door only summarizes the batch, so it answers the same
// baseline the list marks from and never writes — the marks survive every Start visit.
func TestStartVisitSettlesNothing(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{SourcesSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeStart)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.Scope != entities.ReaderScopeStart || act.First {
		t.Fatalf("scope/first = %v/%v, want start/false", act.Scope, act.First)
	}
	if !act.Since.Equal(at(6)) || !act.SeenAt.Equal(at(6)) {
		t.Fatalf("since/seen = %v/%v, want the watermark 6/6", act.Since, act.SeenAt)
	}
	if len(store.writes) != 0 {
		t.Fatalf("start visit wrote %v, want nothing", store.writes)
	}
}

// TestStartVisitOnAFreshVaultMarksNothing: an unacknowledged vault has no baseline, so the front
// door flags nothing and writes nothing either.
func TestStartVisitOnAFreshVaultMarksNothing(t *testing.T) {
	store := &fakeReaderStore{}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeStart)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.First || !act.Since.IsZero() || !act.SeenAt.IsZero() {
		t.Fatalf("first/since/seen = %v/%v/%v, want true/zero/zero", act.First, act.Since, act.SeenAt)
	}
	if len(store.writes) != 0 {
		t.Fatalf("start visit wrote %v, want nothing", store.writes)
	}
}

// TestSourcesVisitMovesTheWatermark: the list answers the batch and acknowledges it — the baseline
// is the previous watermark, so its own rows still show the batch it just settled.
func TestSourcesVisitMovesTheWatermark(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{SourcesSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.First {
		t.Fatal("first = true, want false")
	}
	if !act.Since.Equal(at(6)) || !act.SeenAt.Equal(at(9)) {
		t.Fatalf("since/seen = %v/%v, want 6/9", act.Since, act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].Equal(at(9)) {
		t.Fatalf("writes = %v, want one write at 9", store.writes)
	}
	if !store.state.SourcesSeenAt.Equal(at(9)) {
		t.Fatalf("stored = %v, want 9", store.state.SourcesSeenAt)
	}
}

// TestFirstSourcesVisitInitializesToNowWithoutMarkingTheVault: a fresh vault starts at the
// activation instant so the next arrival is new, and never flags the whole corpus.
func TestFirstSourcesVisitInitializesToNowWithoutMarkingTheVault(t *testing.T) {
	store := &fakeReaderStore{}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.First || !act.Since.IsZero() {
		t.Fatalf("first/since = %v/%v, want true/zero", act.First, act.Since)
	}
	if !act.SeenAt.Equal(at(10)) {
		t.Fatalf("seen_at = %v, want the activation instant", act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].Equal(at(10)) {
		t.Fatalf("writes = %v, want one write at 10", store.writes)
	}
}

// TestFirstVisitNeverStartsBeforeTheNewestActivity: a watermark ahead of the clock (a skewed clock,
// activity recorded later) still settles on the activity, so nothing is lost behind it.
func TestFirstVisitNeverStartsBeforeTheNewestActivity(t *testing.T) {
	store := &fakeReaderStore{}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(8)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.SeenAt.Equal(at(9)) {
		t.Fatalf("seen_at = %v, want the newest activity", act.SeenAt)
	}
}

// TestActivationWithoutANewActivityChangesNothing: the watermark never moves backwards, and an
// activation with nothing later writes and announces nothing.
func TestActivationWithoutANewActivityChangesNothing(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{SourcesSeenAt: at(9)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.First {
		t.Fatal("first = true, want false")
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
	store := &fakeReaderStore{state: entities.ReaderState{SourcesSeenAt: at(7)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	st, err := uc.State(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !st.SourcesSeenAt.Equal(at(7)) {
		t.Fatalf("state = %v, want 7", st.SourcesSeenAt)
	}
	if len(store.writes) != 0 {
		t.Fatalf("state wrote: %v", store.writes)
	}
}
