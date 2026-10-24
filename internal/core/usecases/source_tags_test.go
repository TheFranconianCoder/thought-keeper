package usecases_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

type fakeSourceTags struct {
	byHash map[string][]string
	auto   map[string][]string
	autoAt map[string]time.Time
	index  *fakeIndex
}

func (f *fakeSourceTags) SourceTags(context.Context) (map[string]entities.SourceTagSet, error) {
	out := map[string]entities.SourceTagSet{}
	for hash, tags := range f.byHash {
		out[hash] = entities.SourceTagSet{Tags: tags, AutoTags: f.auto[hash], AutoTagsAt: f.autoAt[hash]}
	}
	for hash, tags := range f.auto {
		if _, ok := out[hash]; !ok {
			out[hash] = entities.SourceTagSet{AutoTags: tags, AutoTagsAt: f.autoAt[hash]}
		}
	}
	return out, nil
}

func (f *fakeSourceTags) SetSourceTags(_ context.Context, hash string, tags []string) error {
	if f.byHash == nil {
		f.byHash = map[string][]string{}
	}
	f.byHash[hash] = append([]string(nil), tags...)
	if f.index != nil {
		return f.index.SetSourceTags(context.Background(), hash, tags)
	}
	return nil
}

func (f *fakeSourceTags) SetSourceAutoTags(_ context.Context, hash string, tags []string, at time.Time) error {
	if f.auto == nil {
		f.auto = map[string][]string{}
	}
	if f.autoAt == nil {
		f.autoAt = map[string]time.Time{}
	}
	f.auto[hash] = append([]string(nil), tags...)
	f.autoAt[hash] = at
	if f.index != nil {
		return f.index.SetSourceAutoTags(context.Background(), hash, tags, at)
	}
	return nil
}

func (f *fakeSourceTags) SourceTagCounts(context.Context) (map[string]int, error) {
	out := map[string]int{}
	for hash := range f.byHash {
		seen := map[string]bool{}
		for _, list := range [][]string{f.byHash[hash], f.auto[hash]} {
			for _, tag := range list {
				if seen[tag] {
					continue
				}
				seen[tag] = true
				out[tag]++
			}
		}
	}
	for hash, tags := range f.auto {
		if _, ok := f.byHash[hash]; ok {
			continue
		}
		for _, tag := range tags {
			out[tag]++
		}
	}
	return out, nil
}

// TestSetSourceTagsNormalizesAndMirrors: a source tag is a human label, but it has the same shape as
// a wiki tag (lowercase, hyphenated) and it is deduplicated. The .meta record is written through the
// tag store; the answer is the source the API shows.
func TestSetSourceTagsNormalizesAndMirrors(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("a1", 32)
	idx := newFakeIndex()
	if err := idx.UpsertSource(ctx, sourceForTags(hash)); err != nil {
		t.Fatalf("source: %v", err)
	}
	tags := &fakeSourceTags{index: idx}
	got, err := (usecases.SetSourceTags{Index: idx, Tags: tags}).Execute(ctx, hash,
		[]string{"  Caching Strategies ", "caching-strategies", "Go", ""})
	if err != nil {
		t.Fatalf("set tags: %v", err)
	}
	want := []string{"caching-strategies", "go"}
	if len(got.Tags) != len(want) || got.Tags[0] != want[0] || got.Tags[1] != want[1] {
		t.Fatalf("tags = %v, want %v", got.Tags, want)
	}
	if len(tags.byHash[hash]) != 2 {
		t.Fatalf("stored = %v, want two labels", tags.byHash[hash])
	}

	// An empty answer clears the labels.
	if _, err := (usecases.SetSourceTags{Index: idx, Tags: tags}).Execute(ctx, hash, nil); err != nil {
		t.Fatalf("clear tags: %v", err)
	}
	if len(tags.byHash[hash]) != 0 {
		t.Fatalf("stored = %v, want none", tags.byHash[hash])
	}
}

func TestSetSourceTagsRejectsTooMany(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("b2", 32)
	idx := newFakeIndex()
	if err := idx.UpsertSource(ctx, sourceForTags(hash)); err != nil {
		t.Fatalf("source: %v", err)
	}
	many := make([]string, 0, 33)
	for i := range 33 {
		many = append(many, fmt.Sprintf("tag-%02d", i))
	}
	if _, err := (usecases.SetSourceTags{Index: idx, Tags: &fakeSourceTags{}}).Execute(ctx, hash, many); err == nil {
		t.Fatal("33 tags accepted, want the cap")
	}
}

func sourceForTags(hash string) entities.Source {
	return entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "A source", Bytes: 1, TreeState: entities.TreeNone,
	}
}

type fakeVocabStore struct {
	v map[entities.VocabularyNamespace]entities.Vocabulary
}

func (f *fakeVocabStore) Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error) {
	if f.v == nil {
		f.v = map[entities.VocabularyNamespace]entities.Vocabulary{}
	}
	if v, ok := f.v[ns]; ok {
		return v, nil
	}
	return entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}, nil
}

func (f *fakeVocabStore) Write(v entities.Vocabulary) error {
	if f.v == nil {
		f.v = map[entities.VocabularyNamespace]entities.Vocabulary{}
	}
	f.v[v.Namespace] = v
	return nil
}

// TestVocabularyReportObservesSourceTags: source labels are a catalog of their own, observed without
// opening a file per source and reported alongside topics and wiki tags.
func TestVocabularyReportObservesSourceTags(t *testing.T) {
	ctx := context.Background()
	report, err := (usecases.VocabularyReport{
		Atoms: &fakeAtomRepo{},
		Sources: &fakeSourceTags{byHash: map[string][]string{
			"a": {"go", "caching"}, "b": {"go"},
		}},
	}).Execute(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	var found bool
	for _, h := range report {
		if h.Namespace != string(entities.VocabularySourceTags) {
			continue
		}
		found = true
		if h.Names != 2 || h.Uses != 3 {
			t.Fatalf("source tags = %+v, want 2 names and 3 uses", h)
		}
	}
	if !found {
		t.Fatalf("no source-tags namespace in %+v", report)
	}
}

// TestApplyVocabularyRenamesSourceTags: a merge is a record write. The source keeps its title, its
// prose and its derivation; only the label changes.
func TestApplyVocabularyRenamesSourceTags(t *testing.T) {
	ctx := context.Background()
	tags := &fakeSourceTags{
		byHash: map[string][]string{
			"a": {"cache-invalidation", "go"},
			"b": {"go"},
		},
		auto: map[string][]string{
			"a": {"cache-invalidation", "cache-invalidation-strategies"},
		},
	}
	vocab := &fakeVocabStore{}
	res, err := (usecases.ApplyVocabularyMapping{
		Namespace: entities.VocabularySourceTags,
		Mapping:   map[string]string{"cache-invalidation": "caching"},
		Vocab:     vocab,
		Sources:   tags,
	}).Execute(ctx)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Sources != 1 {
		t.Fatalf("renamed = %d, want one source", res.Sources)
	}
	if got := tags.byHash["a"]; len(got) != 2 || got[0] != "caching" {
		t.Fatalf("tags of a = %v, want the merged label", got)
	}
	if got := tags.byHash["b"]; len(got) != 1 || got[0] != "go" {
		t.Fatalf("tags of b = %v, want unchanged", got)
	}
	if got := tags.auto["a"]; len(got) != 2 || got[0] != "caching" {
		t.Fatalf("auto tags of a = %v, want the derived label renamed too", got)
	}
}

func sourceForTagging(hash string, state entities.StepState) entities.Source {
	src := sourceForTags(hash)
	src.Steps = entities.StepSet{entities.StepDistill: {State: state}}
	return src
}

// TestTagSourceDerivesCoarseTagsFromTheClaims: the agent proposes the labels; the app writes them into
// the auto-tag set and records when it happened, so the backfill stops asking.
func TestTagSourceDerivesCoarseTagsFromTheClaims(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("c3", 32)
	idx := newFakeIndex()
	if err := idx.UpsertSource(ctx, sourceForTagging(hash, entities.StepOK)); err != nil {
		t.Fatalf("source: %v", err)
	}
	atoms := &fakeAtomRepo{saved: []entities.Atom{
		{ID: ulid.Make(), Topic: "caching", Claim: "A cache key must cover every input.", Body: "b"},
	}}
	tags := &fakeSourceTags{}
	gw := &summaryGateway{answer: `{"tags":["Caching Strategies","go","caching-strategies"]}`}
	uc := usecases.TagSource{
		Index: idx, Atoms: atoms, Tags: tags, Gateway: gw, Model: "test", Clock: fixedClock{},
	}
	res, err := uc.Execute(ctx, ulid.Make(), hash)
	if err != nil {
		t.Fatalf("tag source: %v", err)
	}
	if len(res.Tags) != 2 || res.Tags[0] != "caching-strategies" || res.Tags[1] != "go" {
		t.Fatalf("tags = %v, want the normalized, deduplicated tags", res.Tags)
	}
	if len(tags.auto[hash]) != 2 || tags.autoAt[hash].IsZero() {
		t.Fatalf("stored auto tags = %v at %v, want the labels and the marker", tags.auto[hash], tags.autoAt[hash])
	}
	if !strings.Contains(gw.prompt, "A cache key must cover every input.") {
		t.Fatalf("prompt = %q, want the claims in it", gw.prompt)
	}
}

func TestTagSourceRefusesAnUnfinishedDerivation(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("d4", 32)
	idx := newFakeIndex()
	if err := idx.UpsertSource(ctx, sourceForTagging(hash, entities.StepPending)); err != nil {
		t.Fatalf("source: %v", err)
	}
	tags := &fakeSourceTags{}
	uc := usecases.TagSource{
		Index: idx, Atoms: &fakeAtomRepo{}, Tags: tags, Gateway: &summaryGateway{}, Model: "test", Clock: fixedClock{},
	}
	if _, err := uc.Execute(ctx, ulid.Make(), hash); err == nil {
		t.Fatal("tagging a pending derivation: want an error")
	}
	if len(tags.auto) != 0 {
		t.Fatalf("wrote %v, want nothing", tags.auto)
	}
}

// TestTagSourceMarksASourceWithNoClaims: "tagged, nothing fit" must not be retried forever; the
// timestamp with an empty set is the honest answer.
func TestTagSourceMarksASourceWithNoClaims(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("e5", 32)
	idx := newFakeIndex()
	if err := idx.UpsertSource(ctx, sourceForTagging(hash, entities.StepOK)); err != nil {
		t.Fatalf("source: %v", err)
	}
	tags := &fakeSourceTags{}
	uc := usecases.TagSource{Index: idx, Atoms: &fakeAtomRepo{}, Tags: tags, Clock: fixedClock{}}
	res, err := uc.Execute(ctx, ulid.Make(), hash)
	if err != nil {
		t.Fatalf("tag source: %v", err)
	}
	if len(res.Tags) != 0 || tags.autoAt[hash].IsZero() {
		t.Fatalf("tags = %v at %v, want an empty set with the marker", res.Tags, tags.autoAt[hash])
	}
}
