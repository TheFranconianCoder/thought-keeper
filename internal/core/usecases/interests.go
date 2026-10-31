package usecases

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/interests"
	coremarkdown "github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

const (
	// maxInterestTerms bounds a rule. An interest is a sentence about what the reader wants, not a
	// search engine query; a dozen terms is already a broad subject.
	maxInterestTerms = 12
	// interestPreviewExamples is how many matches the editor shows. The number above them is the
	// whole truth; the examples are for checking the words.
	interestPreviewExamples = 8
)

// InterestInput is what the editor sends. Name and at least one term are required.
type InterestInput struct {
	Name          string
	Terms         []string
	Exclude       []string
	Kinds         []string
	Enabled       bool
	QueueExisting bool
}

// InterestView is one rule as the manager shows it: its definition and its OPEN queue share.
type InterestView struct {
	Interest entities.Interest
	Open     int
}

// IntersectionMatch is one source's result for one rule, as the rail and the preview show it.
type IntersectionMatch struct {
	Slug     string
	Name     string
	Band     string
	Terms    []interests.TermEvidence
	Topics   []string
	Score    int
	Hash     string
	Title    string
	Kind     entities.SourceKind
	Queued   bool
	Rejected bool
	Enabled  bool
}

// InterestPreview is the calibration result: how the rule would read over the vault as it is.
type InterestPreview struct {
	Total    int
	Strong   int
	Weak     int
	Examples []IntersectionMatch
}

// ListInterests reads the rules and counts how many open queue entries each one holds.
type ListInterests struct {
	Store   interfaces.InterestStore
	Counter interfaces.ShelfCounter
}

// Execute returns the rules oldest first.
func (uc ListInterests) Execute(ctx context.Context) ([]InterestView, error) {
	rules, err := uc.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InterestView, 0, len(rules))
	for _, rule := range rules {
		view := InterestView{Interest: rule}
		if uc.Counter != nil {
			if n, cErr := uc.Counter.CountReadLaterVia(ctx, rule.Slug); cErr == nil {
				view.Open = n
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// SaveInterest creates or updates one rule. A rule's slug is its identity: it is derived from the
// name once, and renaming the display name does not move the queue entries that point at it.
type SaveInterest struct {
	Store    interfaces.InterestStore
	Shelf    interfaces.ShelfStore
	Signals  interfaces.InterestSignalReader
	Clock    interfaces.Clock
	OnQueued func(hash string)
}

// Execute writes the rule and, when asked, queues the sources that already match it.
func (uc SaveInterest) Execute(ctx context.Context, slug string, in InterestInput) (entities.Interest, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return entities.Interest{}, types.New("invalid_interest", "an interest needs a name", nil)
	}
	if utf8.RuneCountInString(name) > 80 {
		return entities.Interest{}, types.New("invalid_interest", "the name is longer than 80 characters", nil)
	}
	terms, err := normalizeInterestTerms(in.Terms)
	if err != nil {
		return entities.Interest{}, err
	}
	if len(terms) == 0 {
		return entities.Interest{}, types.New("invalid_interest", "an interest needs at least one term", nil)
	}
	exclude, err := normalizeInterestTerms(in.Exclude)
	if err != nil {
		return entities.Interest{}, err
	}
	kinds, err := normalizeInterestKinds(in.Kinds)
	if err != nil {
		return entities.Interest{}, err
	}
	now := uc.Clock.Now()
	rule := entities.Interest{
		Name: name, Terms: terms, Exclude: exclude, Kinds: kinds,
		Enabled: in.Enabled, Created: now, Updated: now,
	}
	creating := strings.TrimSpace(slug) == ""
	if creating {
		rule.Slug = coremarkdown.Slug(name)
		if rule.Slug == "" || rule.Slug == "untitled" {
			return entities.Interest{}, types.New("invalid_interest", "the name has no usable letters", nil)
		}
		if _, gErr := uc.Store.Get(ctx, rule.Slug); gErr == nil {
			return entities.Interest{}, types.New("conflict", "an interest with this name already exists", types.ErrConflict)
		}
	} else {
		existing, gErr := uc.Store.Get(ctx, slug)
		if gErr != nil {
			return entities.Interest{}, types.New("not_found", "the interest does not exist", types.ErrNotFound)
		}
		rule.Slug = existing.Slug
		rule.Created = existing.Created
		rule.Rejected = existing.Rejected
	}
	if err := uc.Store.Save(ctx, rule); err != nil {
		return entities.Interest{}, err
	}
	if in.QueueExisting && rule.Enabled {
		uc.queueExisting(ctx, rule, now)
	}
	return rule, nil
}

// queueExisting is the one explicit backfill: the human asked for the sources that already fit. It
// is never automatic — a new rule must not flood a queue with material the reader has long read.
func (uc SaveInterest) queueExisting(ctx context.Context, rule entities.Interest, now time.Time) {
	if uc.Signals == nil || uc.Shelf == nil {
		return
	}
	candidates, err := uc.Signals.Candidates(ctx, append(append([]string(nil), rule.Terms...), rule.Exclude...))
	if err != nil {
		return
	}
	for _, c := range candidates {
		if !rule.MatchesKind(c.Kind) || rule.Rejects(c.Hash) {
			continue
		}
		m := interests.Evaluate(rule, c.Signals)
		if m.Band != interests.BandFits {
			continue
		}
		if _, qErr := uc.Shelf.QueueFromInterests(ctx, c.Hash, []string{rule.Slug}, now); qErr == nil && uc.OnQueued != nil {
			uc.OnQueued(c.Hash)
		}
	}
}

// DeleteInterest removes a rule. The queue entries it created stay: they are the reader's queue
// now. Their `via` list keeps a slug no rule answers any more, which the UI simply does not render.
type DeleteInterest struct {
	Store interfaces.InterestStore
}

// Execute deletes one rule.
func (uc DeleteInterest) Execute(ctx context.Context, slug string) error {
	if strings.TrimSpace(slug) == "" {
		return types.New("invalid_interest", "an interest slug is required", nil)
	}
	if _, err := uc.Store.Get(ctx, slug); err != nil {
		return types.New("not_found", "the interest does not exist", types.ErrNotFound)
	}
	return uc.Store.Delete(ctx, slug)
}

// MatchSourceInterests is the automatism: after a source becomes readable (or is distilled), its
// claims, topics, title and labels are compared with every enabled rule, and a STRONG match queues
// it. Only strong matches queue; "maybe" stays a suggestion in the rail.
//
// Two guards keep the queue honest: a source that arrived BEFORE the rule existed is never queued
// (a rule looks forward; the backfill is an explicit checkbox), and a finished entry is never
// re-opened.
type MatchSourceInterests struct {
	Store interfaces.InterestStore
	Index interfaces.IndexRepository
	Atoms interfaces.AtomRepository
	Shelf interfaces.ShelfStore
	Clock interfaces.Clock
}

// MatchSourceResult reports what the pass did.
type MatchSourceResult struct {
	Queued []string // the interest slugs that queued the source
}

// Execute runs the comparison for one source. A source that is still unreadable has nothing to
// compare; that is not an error.
func (uc MatchSourceInterests) Execute(ctx context.Context, hash string) (MatchSourceResult, error) {
	var out MatchSourceResult
	rules, err := uc.Store.List(ctx)
	if err != nil || len(rules) == 0 {
		return out, err
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return out, err
	}
	// A sitemap/feed is a container, not a document: its pages match individually.
	if src.Sitemap || src.RSS {
		return out, nil
	}
	enabled := make([]entities.Interest, 0, len(rules))
	for _, rule := range rules {
		if rule.Enabled && rule.MatchesKind(src.Kind) {
			enabled = append(enabled, rule)
		}
	}
	if len(enabled) == 0 {
		return out, nil
	}
	atoms, err := uc.Atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		return out, err
	}
	text := interests.SourceText{
		Title: src.Title, Tags: src.Tags, AutoTags: src.AutoTags,
		Claims: make([]interests.ClaimText, 0, len(atoms)),
	}
	for _, a := range atoms {
		text.Claims = append(text.Claims, interests.ClaimText{Topic: a.Topic, Claim: a.Claim, Body: a.Body})
	}
	now := uc.Clock.Now()
	slugs := make([]string, 0, len(enabled))
	for _, rule := range enabled {
		if rule.Rejects(hash) {
			continue
		}
		if !rule.Created.IsZero() && src.ImportedAt.Before(rule.Created) {
			continue // the rule looks forward; the backfill is explicit
		}
		if interests.Evaluate(rule, interests.ForSource(text, rule)).Band != interests.BandFits {
			continue
		}
		slugs = append(slugs, rule.Slug)
	}
	if len(slugs) == 0 {
		return out, nil
	}
	changed, err := uc.Shelf.QueueFromInterests(ctx, hash, slugs, now)
	if err != nil {
		return out, err
	}
	if changed {
		out.Queued = slugs
	}
	return out, nil
}

// SourceInterests answers "which rules does this source fit?" for the source page's rail. It is
// computed on demand: the evidence is derived from the claims the vault already has, so it is always
// current — editing a rule does not leave a stale cache behind.
type SourceInterests struct {
	Store interfaces.InterestStore
	Index interfaces.IndexRepository
	Atoms interfaces.AtomRepository
}

// Execute returns both bands (fits and maybe), strongest first.
func (uc SourceInterests) Execute(ctx context.Context, hash string) ([]IntersectionMatch, error) {
	if !entities.IsSHA256Hex(hash) {
		return nil, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	rules, err := uc.Store.List(ctx)
	if err != nil || len(rules) == 0 {
		return nil, err
	}
	atoms, err := uc.Atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		return nil, err
	}
	text := interests.SourceText{
		Title: src.Title, Tags: src.Tags, AutoTags: src.AutoTags,
		Claims: make([]interests.ClaimText, 0, len(atoms)),
	}
	for _, a := range atoms {
		text.Claims = append(text.Claims, interests.ClaimText{Topic: a.Topic, Claim: a.Claim, Body: a.Body})
	}
	open := !src.ReadLaterAt.IsZero() && src.ReadLaterDoneAt.IsZero()
	out := make([]IntersectionMatch, 0, len(rules))
	for _, rule := range rules {
		if !rule.MatchesKind(src.Kind) {
			continue
		}
		m := interests.Evaluate(rule, interests.ForSource(text, rule))
		rejected := rule.Rejects(hash)
		if m.Band == "" && !rejected {
			continue
		}
		if m.Band == "" {
			// A rejected source that no longer matches is still worth showing: the reader's
			// correction has to be visible and undoable.
			m = interests.Match{Band: interests.BandMaybe}
		}
		view := IntersectionMatch{
			Slug: rule.Slug, Name: rule.Name, Band: m.Band, Terms: m.Terms, Topics: m.Topics,
			Score: m.Score, Hash: src.Hash, Title: src.Title, Kind: src.Kind,
			Queued:   open && containsString(src.ReadLaterVia, rule.Slug),
			Rejected: rejected, Enabled: rule.Enabled,
		}
		if view.Rejected {
			view.Queued = false
		}
		out = append(out, view)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Band != out[j].Band {
			return out[i].Band == interests.BandFits
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// RejectSourceInterest records the reader's correction and updates the queue entry it caused.
// Rejecting removes the rule's slug from the entry's via list; if the entry was not manual and no
// interest is left, the entry leaves the queue. Accepting clears the correction and queues the
// source when the rule currently fits it.
type RejectSourceInterest struct {
	Store interfaces.InterestStore
	Index interfaces.IndexRepository
	Shelf interfaces.ShelfStore
	Clock interfaces.Clock
}

// Execute sets the correction. accepted=false means "not this interest".
func (uc RejectSourceInterest) Execute(ctx context.Context, slug, hash string, accepted bool) error {
	if !entities.IsSHA256Hex(hash) {
		return types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	if _, err := uc.Store.SetRejected(ctx, slug, hash, !accepted); err != nil {
		return types.New("not_found", "the interest does not exist", types.ErrNotFound)
	}
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return err
	}
	via := removeString(src.ReadLaterVia, slug)
	now := uc.Clock.Now()
	if accepted {
		// The correction is cleared; if the source is not finished, it joins the queue with this
		// interest as its reason (QueueFromInterests never re-opens a finished entry).
		_, err := uc.Shelf.QueueFromInterests(ctx, hash, []string{slug}, now)
		return err
	}
	if src.ReadLaterAt.IsZero() {
		return nil
	}
	if !src.ReadLaterManual && len(via) == 0 {
		return uc.Shelf.SetReadLater(ctx, hash, time.Time{}, time.Time{}, false, nil)
	}
	return uc.Shelf.SetReadLater(ctx, hash, src.ReadLaterAt, src.ReadLaterDoneAt, src.ReadLaterManual, via)
}

// PreviewInterest is the calibration moment: it reads a draft rule against the vault as it is and
// reports how many sources would fit, how many are only a suggestion, and the strongest examples.
type PreviewInterest struct {
	Signals interfaces.InterestSignalReader
}

// Execute evaluates a draft. It writes nothing.
func (uc PreviewInterest) Execute(ctx context.Context, in InterestInput) (InterestPreview, error) {
	terms, err := normalizeInterestTerms(in.Terms)
	if err != nil {
		return InterestPreview{}, err
	}
	if len(terms) == 0 {
		return InterestPreview{}, types.New("invalid_interest", "an interest needs at least one term", nil)
	}
	exclude, err := normalizeInterestTerms(in.Exclude)
	if err != nil {
		return InterestPreview{}, err
	}
	kinds, err := normalizeInterestKinds(in.Kinds)
	if err != nil {
		return InterestPreview{}, err
	}
	rule := entities.Interest{Name: strings.TrimSpace(in.Name), Terms: terms, Exclude: exclude, Kinds: kinds, Enabled: true}
	candidates, err := uc.Signals.Candidates(ctx, append(append([]string(nil), terms...), exclude...))
	if err != nil {
		return InterestPreview{}, err
	}
	var out InterestPreview
	for _, c := range candidates {
		if !rule.MatchesKind(c.Kind) || rule.Rejects(c.Hash) {
			continue
		}
		m := interests.Evaluate(rule, c.Signals)
		if !m.Matched() {
			continue
		}
		out.Total++
		if m.Band == interests.BandFits {
			out.Strong++
		} else {
			out.Weak++
		}
		out.Examples = append(out.Examples, IntersectionMatch{
			Name: rule.Name, Band: m.Band, Terms: m.Terms, Topics: m.Topics, Score: m.Score,
			Hash: c.Hash, Title: c.Title, Kind: c.Kind, Enabled: true,
		})
	}
	sort.SliceStable(out.Examples, func(i, j int) bool {
		if out.Examples[i].Score != out.Examples[j].Score {
			return out.Examples[i].Score > out.Examples[j].Score
		}
		return out.Examples[i].Hash < out.Examples[j].Hash
	})
	if len(out.Examples) > interestPreviewExamples {
		out.Examples = out.Examples[:interestPreviewExamples]
	}
	return out, nil
}

// normalizeInterestTerms trims, de-duplicates and bounds the words of a rule. Terms keep the
// reader's spelling: they are shown back to them, and only the comparison folds the accents.
func normalizeInterestTerms(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, term := range raw {
		term = strings.Join(strings.Fields(strings.TrimSpace(term)), " ")
		if term == "" {
			continue
		}
		if utf8.RuneCountInString(term) > 80 {
			return nil, types.New("invalid_interest", "an interest term is longer than 80 characters", nil)
		}
		if len([]rune(textmatch.Fold(term))) < 2 {
			return nil, types.New("invalid_interest", "an interest term needs at least two letters", nil)
		}
		key := textmatch.Normalize(term)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, term)
	}
	if len(out) > maxInterestTerms {
		return nil, types.New("invalid_interest", fmt.Sprintf("an interest can carry at most %d terms", maxInterestTerms), nil)
	}
	return out, nil
}

func normalizeInterestKinds(raw []string) ([]string, error) {
	allowed := map[string]bool{
		string(entities.SourceKindMD): true, string(entities.SourceKindPDF): true,
		string(entities.SourceKindTXT): true, string(entities.SourceKindEPUB): true,
		string(entities.SourceKindURL): true,
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, kind := range raw {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			continue
		}
		if !allowed[kind] {
			return nil, types.New("invalid_interest", fmt.Sprintf("unknown source kind %q", kind), nil)
		}
		if !seen[kind] {
			seen[kind] = true
			out = append(out, kind)
		}
	}
	return out, nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func removeString(list []string, value string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}
