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
	writes [][]entities.ReaderWatermark
}

func (f *fakeReaderStore) Read(context.Context) (entities.ReaderState, error) {
	return f.state, nil
}

func (f *fakeReaderStore) WriteScopes(_ context.Context, marks []entities.ReaderWatermark) error {
	for _, mark := range marks {
		if mark.Scope == entities.ReaderScopeSources {
			f.state.SourcesSeenAt = mark.SeenAt
		} else {
			f.state.StartSeenAt = mark.SeenAt
		}
	}
	f.writes = append(f.writes, marks)
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

// settledScopes names the scopes one write settled, so an assertion reads as the rule it checks.
func settledScopes(marks []entities.ReaderWatermark) string {
	names := make([]string, 0, len(marks))
	for _, mark := range marks {
		names = append(names, string(mark.Scope)+"@"+mark.SeenAt.Format("15:04"))
	}
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ","
		}
		out += name
	}
	return out
}

// TestFirstStartVisitInitializesStartAlone: a fresh vault shows no "new" sources or changes, and the
// first Start visit starts that watermark at the activation instant so the next arrival is new. The
// Sources marks stay untouched.
func TestFirstStartVisitInitializesStartAlone(t *testing.T) {
	store := &fakeReaderStore{}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(7)}, Clock: fakeReaderClock{now: at(8)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeStart)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.Scope != entities.ReaderScopeStart || !act.First {
		t.Fatalf("scope/first = %v/%v, want start/true", act.Scope, act.First)
	}
	if !act.Since.IsZero() {
		t.Fatalf("since = %v, want zero: the first open marks nothing", act.Since)
	}
	if !act.SeenAt.Equal(at(8)) {
		t.Fatalf("seen_at = %v, want the activation instant", act.SeenAt)
	}
	if got := settledScopes(act.Settled); got != "start@08:00" {
		t.Fatalf("settled = %s, want start@08:00", got)
	}
	if !store.state.SourcesSeenAt.IsZero() {
		t.Fatalf("sources = %v, want it untouched", store.state.SourcesSeenAt)
	}
}

// TestStartVisitMovesOnlyStart: the baseline is Start's previous watermark and the new one is the
// newest activity, so the client marks exactly the batch it just acknowledged. The Sources marks
// survive — the reader has not opened the list.
func TestStartVisitMovesOnlyStart(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(5), SourcesSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeStart)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if act.First {
		t.Fatal("first = true, want false")
	}
	if !act.Since.Equal(at(5)) || !act.SeenAt.Equal(at(9)) {
		t.Fatalf("since/seen = %v/%v, want 5/9", act.Since, act.SeenAt)
	}
	if got := settledScopes(act.Settled); got != "start@09:00" {
		t.Fatalf("settled = %s, want start@09:00", got)
	}
	if !store.state.SourcesSeenAt.Equal(at(6)) {
		t.Fatalf("sources = %v, want 6 kept", store.state.SourcesSeenAt)
	}
}

// TestSourcesVisitSettlesBothWatermarks: the list answers the same batch the front door summarizes,
// so opening it acknowledges Start too. The Sources marks still show for this phase, because the
// client marks them from the previous watermark.
func TestSourcesVisitSettlesBothWatermarks(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(5), SourcesSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.Since.Equal(at(6)) || !act.SeenAt.Equal(at(9)) {
		t.Fatalf("since/seen = %v/%v, want 6/9", act.Since, act.SeenAt)
	}
	if got := settledScopes(act.Settled); got != "sources@09:00,start@09:00" {
		t.Fatalf("settled = %s, want sources@09:00,start@09:00", got)
	}
	if !store.state.StartSeenAt.Equal(at(9)) || !store.state.SourcesSeenAt.Equal(at(9)) {
		t.Fatalf("state = %v/%v, want both 9", store.state.StartSeenAt, store.state.SourcesSeenAt)
	}
}

// TestFirstSourcesVisitInitializesBothWithoutMarkingTheVault: a first Sources visit starts its own
// watermark at "now" and lifts the front door to the same instant; it never clears a batch Start
// has not shown yet by moving it backwards.
func TestFirstSourcesVisitInitializesBothWithoutMarkingTheVault(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(9)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !act.First || !act.Since.IsZero() {
		t.Fatalf("first/since = %v/%v, want true/zero", act.First, act.Since)
	}
	if !act.SeenAt.Equal(at(10)) {
		t.Fatalf("seen_at = %v, want the activation instant for a scope's first visit", act.SeenAt)
	}
	if got := settledScopes(act.Settled); got != "sources@10:00,start@10:00" {
		t.Fatalf("settled = %s, want both scopes at the activation instant", got)
	}
	if !store.state.StartSeenAt.Equal(at(10)) {
		t.Fatalf("start = %v, want it lifted to the activation instant", store.state.StartSeenAt)
	}
}

// TestSourcesVisitKeepsANewerStartWatermark: settling the front door never moves it backwards.
func TestSourcesVisitKeepsANewerStartWatermark(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(9), SourcesSeenAt: at(6)}}
	uc := usecases.Reader{Store: store, Index: fakeReaderIndex{newest: at(8)}, Clock: fakeReaderClock{now: at(10)}}

	act, err := uc.Activate(context.Background(), entities.ReaderScopeSources)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := settledScopes(act.Settled); got != "sources@08:00" {
		t.Fatalf("settled = %s, want sources@08:00 alone", got)
	}
	if !store.state.StartSeenAt.Equal(at(9)) {
		t.Fatalf("start = %v, want 9 kept", store.state.StartSeenAt)
	}
}

// TestActivationWithoutANewActivityChangesNothing: the watermarks never move backwards and an
// activation with nothing later writes and announces nothing.
func TestActivationWithoutANewActivityChangesNothing(t *testing.T) {
	store := &fakeReaderStore{state: entities.ReaderState{StartSeenAt: at(9), SourcesSeenAt: at(9)}}
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
	if len(act.Settled) != 0 || len(store.writes) != 0 {
		t.Fatalf("settled/writes = %v/%v, want none", act.Settled, store.writes)
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
		t.Fatalf("state wrote: %v", store.writes)
	}
}
