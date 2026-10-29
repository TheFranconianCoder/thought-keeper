package usecases

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// SourceTagReader is what an answer that cites a source's overview needs: the source's labels.
// IndexRepository satisfies it; a test can pass nil and fall back to the question's words.
type SourceTagReader interface {
	GetSourceByHash(ctx context.Context, hash string) (entities.Source, error)
}

// AskTags derives the automatic tags of one saved query.
//
// A query's strongest tags are the topics of the claims it cites: those are the subjects the vault
// itself uses. An answer with no citation (or a legacy answer without links) falls back to the
// meaningful words of the question. The result is bounded and deterministic; it is a label on the
// query list, never a filter on retrieval.
const maxAskTags = 6

var (
	askMarkdownClaim  = regexp.MustCompile(`#/claims/([0-9A-HJKMNP-TV-Z]{26})`)
	askWikiClaim      = regexp.MustCompile(`\[\[claim:([0-9A-HJKMNP-TV-Z]{26})`)
	askMarkdownSource = regexp.MustCompile(`#/sources/([0-9a-f]{64})`)
	askWikiSource     = regexp.MustCompile(`\[\[source:([0-9a-f]{64})`)
	askWord           = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}_-]*`)
)

var askStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "and": true, "are": true,
	"can": true, "could": true, "does": true, "for": true, "from": true, "has": true,
	"have": true, "how": true, "into": true, "its": true, "not": true, "the": true,
	"their": true, "them": true, "there": true, "these": true, "they": true, "this": true,
	"what": true, "when": true, "where": true, "which": true, "why": true, "with": true,
	"would": true, "your": true,
	"ask": true, "query": true, "queries": true, "question": true, "questions": true,
	"saved": true, "save": true, "thought": true, "keeper": true,
	"aber": true, "auch": true, "das": true, "der": true, "die": true, "ein": true,
	"eine": true, "für": true, "ist": true, "mit": true, "nicht": true, "oder": true,
	"sich": true, "und": true, "von": true, "was": true, "wie": true, "wird": true,
	"frage": true, "fragen": true,
}

// AskTags returns the stored tags when there are any, otherwise derives them from the answer's
// claim citations, then from the tags of the sources it cites, and finally from the question's
// words. A digest that cites only source overviews gets the sources' own shelves, not the words of
// the question.
func AskTags(ctx context.Context, question, answer string, atoms interfaces.AtomRepository, sources SourceTagReader) []string {
	tags := tagsFromCitations(ctx, answer, atoms, sources)
	if len(tags) > 0 {
		return tags
	}
	return tagsFromQuestion(question)
}

func tagsFromCitations(ctx context.Context, answer string, atoms interfaces.AtomRepository, sources SourceTagReader) []string {
	counts := map[string]int{}
	if atoms != nil {
		ids := map[string]bool{}
		for _, match := range askMarkdownClaim.FindAllStringSubmatch(answer, -1) {
			ids[match[1]] = true
		}
		for _, match := range askWikiClaim.FindAllStringSubmatch(answer, -1) {
			ids[match[1]] = true
		}
		for id := range ids {
			parsed, err := ulid.ParseStrict(id)
			if err != nil {
				continue
			}
			atom, err := atoms.GetAtom(ctx, parsed)
			if err != nil {
				continue
			}
			topic := markdown.NormalizeTopic(atom.Topic)
			if topic != "" {
				counts[topic]++
			}
		}
	}
	// Claims are the sharper label. Only an answer that cites no claim at all falls back to the
	// sources it cited: a digest's shelves are exactly what the reader filters the Ask list by.
	if len(counts) == 0 && sources != nil {
		hashes := map[string]bool{}
		for _, match := range askMarkdownSource.FindAllStringSubmatch(answer, -1) {
			hashes[match[1]] = true
		}
		for _, match := range askWikiSource.FindAllStringSubmatch(answer, -1) {
			hashes[match[1]] = true
		}
		for hash := range hashes {
			src, err := sources.GetSourceByHash(ctx, hash)
			if err != nil {
				continue
			}
			for _, tag := range src.AllTags() {
				tag = markdown.NormalizeTag(tag)
				if tag != "" {
					counts[tag]++
				}
			}
		}
	}
	return topTags(counts)
}

func tagsFromQuestion(question string) []string {
	counts := map[string]int{}
	for _, word := range askWord.FindAllString(question, -1) {
		word = strings.ToLower(word)
		if len([]rune(word)) < 3 || askStopWords[word] {
			continue
		}
		tag := markdown.NormalizeTag(word)
		if tag == "" {
			continue
		}
		counts[tag]++
	}
	return topTags(counts)
}

func topTags(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	type counted struct {
		tag   string
		count int
	}
	list := make([]counted, 0, len(counts))
	for tag, count := range counts {
		list = append(list, counted{tag, count})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].tag < list[j].tag
	})
	if len(list) > maxAskTags {
		list = list[:maxAskTags]
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.tag)
	}
	return out
}
