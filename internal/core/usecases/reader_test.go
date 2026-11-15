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
	writes []entities.ReaderState
}

func (f *fakeReaderStore) Read(context.Context) (entities.ReaderState, error) {
	return f.state, nil
}

func (f *fakeReaderStore) Write(_ context.Context, state entities.ReaderState) error {
	f.state = state
	f.writes = append(f.writes, state)
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

// TestFirstActivationInitializesOneScopeWithoutMarkingTheWholeVault: a fresh vault shows no "new"
// sources or changes, and the first visit to one lens starts that watermark at the activation
// instant so the next arrival is new. The other lens stays unacknowledged.
func TestFirstActivationInitializesOneScopeWithoutMarkingTheWholeVault(t *testing.T) {
	store := &fakeReaderStore{}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(8)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeStart)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.Scope != entities.ReaderScopeStart || !act.First || !act.Changed {
		t.Fatalf("scope/first/changed = %v/%v/%v, want start/true/true", act.Scope, act.First, act.Changed)
	}
	if !act.Since.IsZero() {
		t.Fatalf("since = %v, want zero: the first open marks nothing", act.Since)
	}
	if !act.SeenAt.Equal(at(8)) {
		t.Fatalf("seen_at = %v, want the activation instant", act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].StartSeenAt.Equal(at(8)) || !store.writes[0].SourcesSeenAt.IsZero() {
		t.Fatalf("writes = %+v, want only Start at the activation instant", store.writes)
	}
}

// TestActivationMovesOnlyItsScope: the baseline is that scope's previous watermark and the new one
// is the newest activity — an arrival or the last content change of a page — so the client marks
// exactly the batch it just acknowledged. The other lens keeps its own instant.
func TestActivationMovesOnlyItsScope(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(5), SourcesSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.First || !act.Changed {
		t.Fatalf("first = %v, changed = %v, want false/true", act.First, act.Changed)
	}
	if !act.Since.Equal(at(6)) || !act.SeenAt.Equal(at(9)) {
		t.Fatalf("since/seen = %v/%v, want 6/9", act.Since, act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].StartSeenAt.Equal(at(5)) || !store.writes[0].SourcesSeenAt.Equal(at(9)) {
		t.Fatalf("writes = %+v, want Start kept and Sources moved", store.writes)
	}
}

// TestFirstSourcesVisitKeepsTheStartWatermark: a Sources acknowledgment clears its own marks only;
// Start's statistic keeps the batch it has not shown yet.
func TestFirstSourcesVisitKeepsTheStartWatermark(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.First || !act.Changed || !act.Since.IsZero() {
		t.Fatalf("first/changed/since = %v/%v/%v, want true/true/zero", act.First, act.Changed, act.Since)
	}
	if !act.SeenAt.Equal(at(10)) {
		t.Fatalf("seen_at = %v, want the activation instant for a scope's first visit", act.SeenAt)
	}
	if len(store.writes) != 1 || !store.writes[0].StartSeenAt.Equal(at(6)) {
		t.Fatalf("writes = %+v, want Start kept", store.writes)
	}
}

// TestActivationWithoutANewActivityChangesNothing: the watermark never moves backwards and an
// activation with nothing later writes and announces nothing.
func TestActivationWithoutANewActivityChangesNothing(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(9), SourcesSeenAt: at(9)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeStart)
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
		t.Fatalf("writes = %+v, want none", store.writes)
	}
}

// TestStateReadsBothWithoutAcknowledging keeps a resync a pure read.
func TestStateReadsBothWithoutAcknowledging(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(6), SourcesSeenAt: at(7)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	st, err := uc.State(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !st.StartSeenAt.Equal(at(6)) || !st.SourcesSeenAt.Equal(at(7)) {
		t.Fatalf("state = %v/%v, want 6/7", st.StartSeenAt, st.SourcesSeenAt)
	}
	if len(store.writes) != 0 {
		t.Fatalf("state wrote: %+v", store.writes)
	}
}
