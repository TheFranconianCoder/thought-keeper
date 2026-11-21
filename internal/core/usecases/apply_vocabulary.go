package usecases

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/claims"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ApplyVocabularyResult reports what the rename touched.
type ApplyVocabularyResult struct {
	Namespace string
	Merged    int
	// Atoms, Pages and Sources are what the rename touched in each namespace: claims of a topic,
	// pages carrying a wiki tag, sources carrying a source tag.
	Atoms     int
	Pages     int
	Sources   int
	Canonical int
	Absorbed  map[string]string
}

// ApplyMapping merges names: it is a rename and nothing is derived again.
func (uc Vocabulary) ApplyMapping(ctx context.Context) (ApplyVocabularyResult, error) {
	res := ApplyVocabularyResult{Namespace: string(uc.Namespace)}
	if uc.Vocab == nil {
		return res, fmt.Errorf("vocabulary: no store configured")
	}
	if uc.Namespace != entities.VocabularyTopics && uc.Namespace != entities.VocabularyWikiTags &&
		uc.Namespace != entities.VocabularySourceTags {
		return res, fmt.Errorf("vocabulary: unknown namespace %q", uc.Namespace)
	}
	mapping := resolveMapping(uc.Mapping)
	res.Absorbed = mapping
	// The names this question ASKED about and did not merge are a decision too: the shelf cannot
	// honestly cover them. They go into Decided in the same write as the mapping, so the next pass
	// does not ask them again — and a batch that files nothing still moves the tail forward.
	judged := make([]string, 0, len(uc.Judged))
	for _, name := range uc.Judged {
		if _, filed := mapping[name]; filed {
			continue
		}
		judged = append(judged, name)
	}
	if len(mapping) == 0 && len(uc.Canonical) == 0 && len(judged) == 0 {
		return res, nil
	}

	var n int
	var err error
	switch uc.Namespace {
	case entities.VocabularyWikiTags:
		n, err = uc.renamePageTags(ctx, mapping)
		res.Pages = n
	case entities.VocabularySourceTags:
		n, err = uc.renameSourceTags(ctx, mapping)
		res.Sources = n
	default:
		n, err = uc.renameClaimTopics(ctx, mapping)
		res.Atoms = n
	}
	if err != nil {
		return res, err
	}
	res.Merged = len(mapping)

	v, err := uc.Vocab.Read(uc.Namespace)
	if err != nil {
		return res, err
	}
	if v.Absorbed == nil {
		v.Absorbed = map[string]string{}
	}
	maps.Copy(v.Absorbed, mapping)
	if len(judged) > 0 {
		v.Decided = appendDecided(v.Decided, judged)
	}
	if len(uc.Canonical) > 0 {
		v.Canonical = uc.Canonical
	} else if len(mapping) > 0 {
		v.Canonical = mergeCanonical(v.Canonical, mapping)
	}
	if uc.Clock != nil {
		v.Updated = uc.Clock.Now()
	}
	v.Run = uc.RunID
	if err := uc.Vocab.Write(v); err != nil {
		return res, err
	}
	res.Canonical = len(v.Canonical)
	if uc.Log != nil {
		uc.Log.Info("vocabulary merged", "namespace", res.Namespace, "names", res.Merged,
			"judged", len(judged), "atoms", res.Atoms, "canonical", res.Canonical)
	}
	return res, nil
}

// appendDecided adds names to the vocabulary's memory of judged names, once each: a name the model
// has answered for is never a question again.
func appendDecided(decided, names []string) []string {
	seen := make(map[string]bool, len(decided)+len(names))
	out := decided
	for _, name := range decided {
		seen[name] = true
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// renameClaimTopics rewrites the claims' flat topic. The claim keeps everything else: a subject was
// renamed, not rewritten. The FILE is written claim by claim (it is the source of truth) and the
// index follows in ONE transaction: a topic moves no provenance and no search text, so the per-claim
// transaction SaveAtom opens is pure overhead — it is what made a coarsening of a real vault take
// hours.
func (uc Vocabulary) renameClaimTopics(ctx context.Context, mapping map[string]string) (int, error) {
	n := 0
	topics := make(map[ulid.ULID]string, len(mapping)*4)
	for from, to := range mapping {
		if claims.TopicIsStructural(to) {
			if uc.Log != nil {
				uc.Log.Warn("vocabulary: structural topic rejected", "from", from, "to", to)
			}
			continue
		}
		atoms, err := uc.Atoms.ListAtoms(ctx, from, 100000, 0)
		if err != nil {
			return n, fmt.Errorf("list claims of %q: %w", from, err)
		}
		for _, a := range atoms {
			a.Topic = to
			if uc.AtomStore != nil {
				// The store normalizes the name and answers what it wrote, so the index records the
				// same topic the file carries.
				written, wErr := uc.AtomStore.Write(ctx, a)
				if wErr != nil {
					return n, fmt.Errorf("write claim: %w", wErr)
				}
				a = written
			}
			topics[a.ID] = a.Topic
			n++
		}
	}
	if err := uc.Atoms.RenameAtomTopics(ctx, topics); err != nil {
		return n, fmt.Errorf("index claim topics: %w", err)
	}
	return n, nil
}

// renamePageTags rewrites the tags in every page's frontmatter. The page's prose, its source and its
// claims are untouched: a tag is a label on the page, not part of what it says.
func (uc Vocabulary) renamePageTags(ctx context.Context, mapping map[string]string) (int, error) {
	if uc.Wiki == nil || len(mapping) == 0 {
		return 0, nil
	}
	pages, err := uc.Wiki.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("list wiki pages: %w", err)
	}
	renamed := 0
	for _, page := range pages {
		before := append(append([]string(nil), page.Tags...), page.AutoTags...)
		page.Tags = rewriteTags(page.Tags, mapping)
		page.AutoTags = rewriteTags(page.AutoTags, mapping)
		after := append(append([]string(nil), page.Tags...), page.AutoTags...)
		if strings.Join(before, "\x00") == strings.Join(after, "\x00") {
			continue
		}
		if uc.Clock != nil {
			page.UpdatedAt = uc.Clock.Now()
		}
		if err := uc.Wiki.Write(ctx, page); err != nil {
			return renamed, fmt.Errorf("write page %s: %w", page.ID, err)
		}
		renamed++
	}
	return renamed, nil
}

// renameSourceTags rewrites the labels in every source's record. The source's title, prose and
// derivation are untouched: a tag is a label on the row, not part of what it says.
func (uc Vocabulary) renameSourceTags(ctx context.Context, mapping map[string]string) (int, error) {
	if uc.Sources == nil || len(mapping) == 0 {
		return 0, nil
	}
	all, err := uc.Sources.SourceTags(ctx)
	if err != nil {
		return 0, fmt.Errorf("list source tags: %w", err)
	}
	renamed := 0
	for hash, set := range all {
		human := rewriteTags(set.Tags, mapping)
		auto := rewriteTags(set.AutoTags, mapping)
		humanChanged := strings.Join(set.Tags, "\x00") != strings.Join(human, "\x00")
		autoChanged := strings.Join(set.AutoTags, "\x00") != strings.Join(auto, "\x00")
		if !humanChanged && !autoChanged {
			continue
		}
		if humanChanged {
			if err := uc.Sources.SetSourceTags(ctx, hash, human); err != nil {
				return renamed, fmt.Errorf("write tags of %s: %w", hash, err)
			}
		}
		if autoChanged {
			if err := uc.Sources.SetSourceAutoTags(ctx, hash, auto, set.AutoTagsAt); err != nil {
				return renamed, fmt.Errorf("write auto tags of %s: %w", hash, err)
			}
		}
		renamed++
	}
	return renamed, nil
}

// rewriteTags applies a mapping to a tag list, drops duplicates the merge created, and keeps the
// reader's order.
func rewriteTags(tags []string, mapping map[string]string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		if to, ok := mapping[tag]; ok {
			tag = to
		}
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

// resolveMapping drops the entries that say nothing and follows a chain to its end (a→b, b→c means
// a→c), so a mapping a model proposed can be applied as it came.
func resolveMapping(in map[string]string) map[string]string {
	out := map[string]string{}
	for from := range in {
		to := in[from]
		for range 8 {
			next, ok := in[to]
			if !ok || next == to {
				break
			}
			to = next
		}
		if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" || from == to {
			continue
		}
		out[from] = to
	}
	return out
}

// mergeCanonical keeps the canonical set as it was, minus what was absorbed, plus what absorbed it.
func mergeCanonical(canonical []string, mapping map[string]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(canonical)+len(mapping))
	for _, name := range canonical {
		if _, absorbed := mapping[name]; absorbed {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, to := range mapping {
		if seen[to] {
			continue
		}
		seen[to] = true
		out = append(out, to)
	}
	return out
}
