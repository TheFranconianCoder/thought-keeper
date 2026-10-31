package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

type fakeInterestStore struct {
	m map[string]entities.Interest
}

func newFakeInterests(rules ...entities.Interest) *fakeInterestStore {
	out := &fakeInterestStore{m: map[string]entities.Interest{}}
	for _, rule := range rules {
		out.m[rule.Slug] = rule
	}
	return out
}

func (f *fakeInterestStore) List(context.Context) ([]entities.Interest, error) {
	out := make([]entities.Interest, 0, len(f.m))
	for _, rule := range f.m {
		out = append(out, rule)
	}
	return out, nil
}

func (f *fakeInterestStore) Get(_ context.Context, slug string) (entities.Interest, error) {
	rule, ok := f.m[slug]
	if !ok {
		return entities.Interest{}, context.Canceled // any error; the use case maps it
	}
	return rule, nil
}

func (f *fakeInterestStore) Save(_ context.Context, rule entities.Interest) error {
	f.m[rule.Slug] = rule
	return nil
}

func (f *fakeInterestStore) Delete(_ context.Context, slug string) error {
	delete(f.m, slug)
	return nil
}

func (f *fakeInterestStore) SetRejected(_ context.Context, slug, hash string, rejected bool) (entities.Interest, error) {
	rule, ok := f.m[slug]
	if !ok {
		return entities.Interest{}, context.Canceled
	}
	out := make([]string, 0, len(rule.Rejected)+1)
	for _, h := range rule.Rejected {
		if h != hash {
			out = append(out, h)
		}
	}
	if rejected {
		out = append(out, hash)
	}
	rule.Rejected = out
	f.m[slug] = rule
	return rule, nil
}

// fakeShelf mirrors the tracker: the index row and the record move together.
type fakeShelf struct {
	index *fakeIndex
}

func (f *fakeShelf) SetFavorite(_ context.Context, hash string, at time.Time) error {
	src := f.index.m[hash]
	src.FavoriteAt = at
	f.index.m[hash] = src
	return nil
}

func (f *fakeShelf) SetReadLater(_ context.Context, hash string, at, doneAt time.Time, manual bool, via []string) error {
	src := f.index.m[hash]
	src.ReadLaterAt, src.ReadLaterDoneAt, src.ReadLaterManual, src.ReadLaterVia = at, doneAt, manual, via
	f.index.m[hash] = src
	return nil
}

func (f *fakeShelf) QueueFromInterests(_ context.Context, hash string, via []string, at time.Time) (bool, error) {
	src := f.index.m[hash]
	if !src.ReadLaterAt.IsZero() && !src.ReadLaterDoneAt.IsZero() {
		return false, nil
	}
	changed := false
	if src.ReadLaterAt.IsZero() {
		src.ReadLaterAt, src.ReadLaterDoneAt = at, time.Time{}
		changed = true
	}
	seen := map[string]bool{}
	for _, slug := range src.ReadLaterVia {
		seen[slug] = true
	}
	for _, slug := range via {
		if !seen[slug] {
			src.ReadLaterVia = append(src.ReadLaterVia, slug)
			seen[slug] = true
			changed = true
		}
	}
	f.index.m[hash] = src
	return changed, nil
}

func interestSource(hash string, imported time.Time) entities.Source {
	return entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "The Tokio scheduler", ImportedAt: imported,
		TreeState: entities.TreeBuilt,
	}
}

func TestMatchSourceInterestsQueuesOnlyStrongForwardMatches(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := fixedClock{}.Now()
	rule := entities.Interest{
		Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"},
		Enabled: true, Created: now.Add(-time.Hour),
	}
	index := newFakeIndex()
	index.m[hash] = interestSource(hash, now)
	atoms := &fakeAtomRepo{saved: []entities.Atom{
		{Topic: "async-runtime", Claim: "The tokio scheduler is cooperative.", Body: ""},
		{Topic: "async-runtime", Claim: "The tokio runtime steals work.", Body: ""},
	}}
	shelf := &fakeShelf{index: index}
	uc := usecases.MatchSourceInterests{
		Store: newFakeInterests(rule), Index: index, Atoms: atoms, Shelf: shelf, Clock: fixedClock{},
	}
	res, err := uc.Execute(context.Background(), hash)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(res.Queued) != 1 || res.Queued[0] != "rust-async" {
		t.Fatalf("queued = %v, want the rule", res.Queued)
	}
	src := index.m[hash]
	if src.ReadLaterAt.IsZero() || src.ReadLaterDoneAt != (time.Time{}) {
		t.Fatalf("queue entry = %+v", src)
	}
	if len(src.ReadLaterVia) != 1 || src.ReadLaterVia[0] != "rust-async" {
		t.Fatalf("via = %v", src.ReadLaterVia)
	}
}

func TestMatchSourceInterestsNeverQueuesWhatCameBeforeTheRule(t *testing.T) {
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	now := fixedClock{}.Now()
	rule := entities.Interest{
		Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"},
		Enabled: true, Created: now,
	}
	index := newFakeIndex()
	index.m[hash] = interestSource(hash, now.Add(-24*time.Hour))
	atoms := &fakeAtomRepo{saved: []entities.Atom{
		{Topic: "async-runtime", Claim: "Tokio schedules.", Body: ""},
		{Topic: "async-runtime", Claim: "Tokio steals work.", Body: ""},
	}}
	shelf := &fakeShelf{index: index}
	uc := usecases.MatchSourceInterests{Store: newFakeInterests(rule), Index: index, Atoms: atoms, Shelf: shelf, Clock: fixedClock{}}
	res, err := uc.Execute(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Queued) != 0 {
		t.Fatalf("old source was queued: %v", res.Queued)
	}
	if !index.m[hash].ReadLaterAt.IsZero() {
		t.Fatal("an old source entered the queue")
	}
}

func TestMatchSourceInterestsLeavesOneMentionAsASuggestion(t *testing.T) {
	hash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	now := fixedClock{}.Now()
	rule := entities.Interest{Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"}, Enabled: true, Created: now.Add(-time.Hour)}
	index := newFakeIndex()
	src := interestSource(hash, now)
	src.Title = "A scheduling note"
	index.m[hash] = src
	atoms := &fakeAtomRepo{saved: []entities.Atom{{Topic: "misc", Claim: "A passing mention of tokio.", Body: ""}}}
	shelf := &fakeShelf{index: index}
	uc := usecases.MatchSourceInterests{Store: newFakeInterests(rule), Index: index, Atoms: atoms, Shelf: shelf, Clock: fixedClock{}}
	res, _ := uc.Execute(context.Background(), hash)
	if len(res.Queued) != 0 || !index.m[hash].ReadLaterAt.IsZero() {
		t.Fatalf("a single mention queued the source: %+v", res)
	}
}

func TestMatchSourceInterestsHonoursTheRejection(t *testing.T) {
	hash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := fixedClock{}.Now()
	rule := entities.Interest{
		Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"}, Enabled: true,
		Created: now.Add(-time.Hour), Rejected: []string{hash},
	}
	index := newFakeIndex()
	index.m[hash] = interestSource(hash, now)
	atoms := &fakeAtomRepo{saved: []entities.Atom{
		{Topic: "async-runtime", Claim: "Tokio schedules.", Body: ""},
		{Topic: "async-runtime", Claim: "Tokio steals work.", Body: ""},
	}}
	shelf := &fakeShelf{index: index}
	uc := usecases.MatchSourceInterests{Store: newFakeInterests(rule), Index: index, Atoms: atoms, Shelf: shelf, Clock: fixedClock{}}
	res, _ := uc.Execute(context.Background(), hash)
	if len(res.Queued) != 0 {
		t.Fatalf("a rejected source was queued: %v", res.Queued)
	}
}

func TestSourceInterestsShowsEvidenceAndTheQueueState(t *testing.T) {
	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	now := fixedClock{}.Now()
	rule := entities.Interest{Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"}, Enabled: true, Created: now.Add(-time.Hour)}
	index := newFakeIndex()
	src := interestSource(hash, now)
	src.ReadLaterAt = now
	src.ReadLaterVia = []string{"rust-async"}
	index.m[hash] = src
	atoms := &fakeAtomRepo{saved: []entities.Atom{
		{Topic: "async-runtime", Claim: "Tokio schedules.", Body: ""},
		{Topic: "async-runtime", Claim: "Tokio steals work.", Body: ""},
	}}
	uc := usecases.SourceInterests{Store: newFakeInterests(rule), Index: index, Atoms: atoms}
	matches, err := uc.Execute(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || !matches[0].Queued || matches[0].Band != "fits" {
		t.Fatalf("matches = %+v", matches)
	}
	if len(matches[0].Terms) != 1 || matches[0].Terms[0].Claims != 2 {
		t.Fatalf("evidence = %+v", matches[0].Terms)
	}
}

func TestRejectingAnInterestRemovesTheAutomaticQueueEntry(t *testing.T) {
	hash := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	now := fixedClock{}.Now()
	rule := entities.Interest{Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"}, Enabled: true, Created: now}
	index := newFakeIndex()
	src := interestSource(hash, now)
	src.ReadLaterAt = now
	src.ReadLaterVia = []string{"rust-async"}
	index.m[hash] = src
	shelf := &fakeShelf{index: index}
	store := newFakeInterests(rule)
	uc := usecases.RejectSourceInterest{Store: store, Index: index, Shelf: shelf, Clock: fixedClock{}}
	if err := uc.Execute(context.Background(), "rust-async", hash, false); err != nil {
		t.Fatal(err)
	}
	after := index.m[hash]
	if !after.ReadLaterAt.IsZero() {
		t.Fatalf("the automatic entry survived the rejection: %+v", after)
	}
	if !store.m["rust-async"].Rejects(hash) {
		t.Fatal("the rejection was not recorded")
	}
}

func TestASourceKeptByHandStaysAfterARejection(t *testing.T) {
	hash := "1111111111111111111111111111111111111111111111111111111111111111"
	now := fixedClock{}.Now()
	rule := entities.Interest{Slug: "rust-async", Name: "Rust async", Terms: []string{"tokio"}, Enabled: true, Created: now}
	index := newFakeIndex()
	src := interestSource(hash, now)
	src.ReadLaterAt, src.ReadLaterManual, src.ReadLaterVia = now, true, []string{"rust-async"}
	index.m[hash] = src
	shelf := &fakeShelf{index: index}
	uc := usecases.RejectSourceInterest{Store: newFakeInterests(rule), Index: index, Shelf: shelf, Clock: fixedClock{}}
	if err := uc.Execute(context.Background(), "rust-async", hash, false); err != nil {
		t.Fatal(err)
	}
	after := index.m[hash]
	if after.ReadLaterAt.IsZero() || len(after.ReadLaterVia) != 0 {
		t.Fatalf("manual entry = %+v, want it kept without the interest", after)
	}
}

func TestSaveInterestCreatesASlugAndRejectsADuplicate(t *testing.T) {
	store := newFakeInterests()
	uc := usecases.SaveInterest{Store: store, Clock: fixedClock{}}
	rule, err := uc.Execute(context.Background(), "", usecases.InterestInput{
		Name: "Rust Async", Terms: []string{"rust", "Rust ", "async"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rule.Slug != "rust-async" {
		t.Fatalf("slug = %q", rule.Slug)
	}
	if len(rule.Terms) != 2 {
		t.Fatalf("terms = %v, want the duplicates dropped", rule.Terms)
	}
	if _, err := uc.Execute(context.Background(), "", usecases.InterestInput{
		Name: "Rust async", Terms: []string{"rust"}, Enabled: true,
	}); err == nil {
		t.Fatal("a duplicate name was accepted")
	}
}
