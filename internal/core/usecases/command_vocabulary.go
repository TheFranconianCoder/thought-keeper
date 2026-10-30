package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// The assistant is a MAPPER, not a reader.
//
// Every source already carries tags an LLM derived from its claims, and every claim carries a flat
// topic. That is the vault's semantic layer — curated, normalised, with an alias table — and it is
// what makes "sources about software architecture" answerable without reading a single document:
// the tag `software-architecture` alone covers 67 of them.
//
// So a request about a subject is resolved in three cheap steps:
//
//	words → terms → NAMES in the vault's vocabulary → the set (deterministic joins)
//
// The model does only the first arrow: it is good at the thing the vocabulary cannot do by itself —
// translation and synonyms ("Architektur" → architecture, "craftsmanship" → software-craftsmanship).
// It never invents a name: the server matches its terms against the REAL names and keeps what exists.
// Without a model the same matching runs on the request's own words (word similarity over the
// names), which already finds the tag when the wording happens to agree.
//
// Reading content is gone on purpose: it was slow, it could not be reproduced, and it was worse than
// the metadata the vault already had.

// nameUses is one name of the vocabulary and how often the vault uses it.
type nameUses struct {
	Name  string
	Count int
}

// mapping is what a request's words were resolved to: the names, and the terms that found them.
type mapping struct {
	Tags   []nameUses
	Topics []nameUses
	// Terms are the words the match ran with — the model's expansion plus the request's own words.
	Terms []string
}

// Empty reports whether nothing in the vocabulary matched.
func (m mapping) Empty() bool { return len(m.Tags) == 0 && len(m.Topics) == 0 }

// namespace caps: a set built from ten tags is not a set, it is a shrug. Three names per namespace,
// strongest first, keeps the answer sharp and the rationale readable.
const (
	maxMatchedTags   = 3
	maxMatchedTopics = 3
)

// resolveByVocabulary maps the request onto the vault's names and materializes the set.
func (uc RunCommand) resolveByVocabulary(ctx context.Context, cmd *entities.Command) error {
	terms, modelFailed := uc.requestTerms(ctx, cmd.Text, cmd.Lens)
	terms = uc.expandAliases(terms)
	// The words became NAMES. The free text must not survive as a criterion: it was never a
	// substring filter, and leaving it in place filtered out every mapped source.
	cmd.Criteria.Text = ""
	cmd.Criteria.Tags = nil
	cmd.Criteria.Topics = nil
	if modelFailed {
		cmd.Notes = append(cmd.Notes,
			"the model did not answer; the match ran on the request's own words")
	}

	tagCounts := map[string]int{}
	if counts, err := uc.Index.SourceTagCounts(ctx); err == nil {
		tagCounts = counts
	}
	topicCounts := map[string]int{}
	if uses, err := uc.Atoms.ListClaimTopics(ctx); err == nil {
		for _, u := range uses {
			topicCounts[u.Topic] = u.Count
		}
	}

	mapped := mapping{
		Terms:  terms,
		Tags:   matchNames(terms, tagCounts, maxMatchedTags),
		Topics: matchNames(terms, topicCounts, maxMatchedTopics),
	}
	// A claim list is built from topics; a source list from tags AND the provenance of the topics.
	if cmd.Lens == entities.CommandClaims {
		// A tag says nothing about a claim; only topics do.
		mapped.Tags = nil
	}

	// The names travel with the record for EVERY lens: they are the reading the banner shows and the
	// reason "run again" is cheap.
	for _, t := range mapped.Tags {
		cmd.Criteria.Tags = append(cmd.Criteria.Tags, t.Name)
	}
	for _, t := range mapped.Topics {
		cmd.Criteria.Topics = append(cmd.Criteria.Topics, t.Name)
	}

	if mapped.Empty() {
		cmd.Total, cmd.Vault, cmd.Scanned = 0, 0, 0
		cmd.Notes = append(cmd.Notes, fmt.Sprintf(
			"no tag or topic of this vault matches %s", strings.Join(terms, ", ")))
		return nil
	}

	switch cmd.Lens {
	case entities.CommandWiki:
		return uc.wikiFromTags(ctx, cmd, mapped)
	default:
		return uc.sourcesFromMapping(ctx, cmd, mapped)
	}
}

// sourcesFromMapping materializes a SOURCE set from the mapped names: the sources tagged with them,
// plus the sources whose claims carry the mapped topics (provenance). Both are joins, not reads.
func (uc RunCommand) sourcesFromMapping(ctx context.Context, cmd *entities.Command, mapped mapping) error {
	counts := map[string]int{}
	found := map[string]int{}
	for _, tag := range mapped.Tags {
		srcs, err := uc.Index.ListSources(ctx, entities.SourceFilter{Tag: tag.Name}, uc.scanLimit(), 0)
		if err != nil {
			continue
		}
		for _, s := range srcs {
			counts[s.Hash] += 2 // a tag is a coarser, surer signal than one claim's topic
			found["tag:"+tag.Name]++
		}
	}
	for _, topic := range mapped.Topics {
		atoms, err := uc.Atoms.ListAtoms(ctx, topic.Name, uc.scanLimit(), 0)
		if err != nil {
			continue
		}
		for _, a := range atoms {
			found["topic:"+topic.Name]++
			for _, src := range a.Sources {
				if src.Hash != "" {
					counts[src.Hash]++
				}
			}
		}
	}
	if cmd.Lens == entities.CommandClaims {
		// Claims are the items themselves, not their sources.
		return uc.claimsFromTopics(ctx, cmd, mapped)
	}
	cmd.Mapped = mappedNames(mapped, found)
	if err := uc.resolveSourcesFromCounts(ctx, cmd, counts); err != nil {
		return err
	}
	// No rationale on a source list: the banner carries the names as a text line and the notes carry
	// the evidence. A dense list does not need a sentence repeating both.
	return nil
}

// claimsFromTopics materializes a CLAIM set from the mapped topics.
func (uc RunCommand) claimsFromTopics(ctx context.Context, cmd *entities.Command, mapped mapping) error {
	items := make([]entities.CommandItem, 0, uc.maxItems())
	total := 0
	found := map[string]int{}
	for _, topic := range mapped.Topics {
		atoms, err := uc.Atoms.ListAtoms(ctx, topic.Name, uc.scanLimit(), 0)
		if err != nil {
			continue
		}
		total += len(atoms)
		for _, a := range atoms {
			if len(items) >= uc.maxItems() {
				break
			}
			items = append(items, entities.CommandItem{Ref: a.ID.String(), Title: a.Claim, Kind: a.Topic})
			found["topic:"+topic.Name]++
		}
	}
	cmd.Items = items
	cmd.Total, cmd.Vault, cmd.Scanned = total, total, total
	cmd.Mapped = mappedNames(mapped, found)
	return nil
}

// wikiFromTags materializes a WIKI set from the mapped tags: a page carries the same coarse labels.
func (uc RunCommand) wikiFromTags(ctx context.Context, cmd *entities.Command, mapped mapping) error {
	if uc.Wiki == nil {
		return nil
	}
	pages, err := uc.Wiki.List(ctx)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, tag := range mapped.Tags {
		wanted[strings.ToLower(tag.Name)] = true
	}
	items := make([]entities.CommandItem, 0, uc.maxItems())
	found := map[string]int{}
	for _, page := range pages {
		if len(items) >= uc.maxItems() {
			break
		}
		for _, tag := range append(append([]string{}, page.Tags...), page.AutoTags...) {
			lower := strings.ToLower(tag)
			if !wanted[lower] {
				continue
			}
			items = append(items, entities.CommandItem{
				Ref: page.ID.String(), Title: page.Title, Kind: "wiki",
				Why: fmt.Sprintf("tagged %q", tag),
			})
			found["tag:"+lower]++
			break
		}
	}
	cmd.Items = items
	cmd.Total, cmd.Vault, cmd.Scanned = len(items), len(pages), len(pages)
	cmd.Mapped = mappedNames(mapped, found)
	return nil
}

// requestTerms produces the words the match runs with: the request's own meaningful words, plus —
// when a model is configured — a small expansion for translation and synonyms.
func (uc RunCommand) requestTerms(ctx context.Context, text string, lens entities.CommandLens) ([]string, bool) {
	terms := meaningfulWords(text)
	if uc.Gateway == nil || uc.Model == "" {
		return terms, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	system, user := agents.CommandTermsMessages(text, lens, terms)
	resp, err := uc.Gateway.Complete(ctx, interfaces.AgentRequest{
		Model:    uc.Model,
		Messages: []interfaces.AgentMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
	})
	if err != nil {
		return terms, true
	}
	var answer struct {
		Terms []string `json:"terms"`
	}
	body := resp.Message.Content
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	if json.Unmarshal([]byte(body), &answer) != nil {
		return terms, true
	}
	for _, t := range answer.Terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || len(t) > 60 {
			continue
		}
		terms = append(terms, t)
	}
	return dedupeTerms(terms), false
}

// expandAliases adds the canonical name behind every curated merge. A vocabulary that absorbed
// "cloud-computing" into "cloud" left an alias, and a request that still says the old name must
// still land on the new one — that is the whole point of a curated vocabulary.
func (uc RunCommand) expandAliases(terms []string) []string {
	if uc.Vocab == nil {
		return terms
	}
	out := append([]string(nil), terms...)
	for _, ns := range []entities.VocabularyNamespace{entities.VocabularySourceTags, entities.VocabularyTopics, entities.VocabularyWikiTags} {
		v, err := uc.Vocab.Read(ns)
		if err != nil {
			continue
		}
		for absorbed, canonical := range v.Absorbed {
			old := strings.ToLower(strings.TrimSpace(absorbed))
			if old == "" {
				continue
			}
			for _, term := range terms {
				if strings.Join(splitName(term), " ") == strings.Join(splitName(old), " ") {
					out = append(out, strings.ToLower(canonical))
				}
			}
		}
	}
	return dedupeTerms(out)
}

// matchNames keeps the NAMES that a term actually matches, strongest first. This is the fallback
// and the whole matcher: word similarity over the vault's own names, never over its content.
func matchNames(terms []string, names map[string]int, limit int) []nameUses {
	if len(terms) == 0 || len(names) == 0 {
		return nil
	}
	// Document frequency over the name set: a word that appears in ONE name identifies it, a word
	// that appears in many identifies nothing. "architecture" must beat "software" when the request
	// says both — without it, "software" drags every software-* tag in.
	df := map[string]int{}
	for name := range names {
		for _, tok := range splitName(name) {
			df[tok]++
		}
	}
	termTokens := make([][]string, 0, len(terms))
	for _, t := range terms {
		termTokens = append(termTokens, splitName(t))
	}
	type scored struct {
		name  string
		count int
		score int
	}
	out := make([]scored, 0, 16)
	for name, count := range names {
		tokens := splitName(name)
		best := 0
		for _, tt := range termTokens {
			if s := nameScore(tt, tokens, df); s > best {
				best = s
			}
		}
		if best >= 40 {
			out = append(out, scored{name: name, count: count, score: best})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].count > out[j].count
	})
	if len(out) > limit {
		out = out[:limit]
	}
	res := make([]nameUses, 0, len(out))
	for _, s := range out {
		res = append(res, nameUses{Name: s.name, Count: s.count})
	}
	return res
}

// nameTokens splits a name into words: "software-architecture" → [software architecture].
func splitName(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ").Replace(s)
	return strings.Fields(s)
}

// nameScore grades how well a term matches a name. Exact beats a shared token, a shared token beats
// a substring; anything weaker is noise and is not a match.
func nameScore(term, name []string, df map[string]int) int {
	if len(term) == 0 || len(name) == 0 {
		return 0
	}
	if strings.Join(term, " ") == strings.Join(name, " ") {
		return 200
	}
	// Every word of the term appears in the name (or the other way round): "software" ⊂
	// "software-architecture", "software architecture" ⊂ "software-architecture-patterns".
	if subset(term, name) || subset(name, term) {
		return 70 + int(60*specificity(term, df))
	}
	for _, t := range term {
		for _, n := range name {
			// The same word after a plural or a German suffix: architectures / architecture.
			if len(t) >= 5 && stem(t) == stem(n) {
				return 95
			}
			// One typo, or two in a long word: kubernetes / kubernetis.
			if len(t) >= 5 && len(n) >= 5 {
				if d := levenshtein(t, n); d <= 1 || (d <= 2 && len(t) >= 8) {
					return 60
				}
			}
			// The same sound: Müller / Miller, Schmidt / Schmitt. Gated hard — a phonetic match is
			// the weakest kind and must not turn "architektur" into "architecture".
			if len(t) >= 5 && len(n) >= 5 && t[0] == n[0] && koelner(t) == koelner(n) {
				return 45
			}
			// A long shared word, deliberately conservative: four letters match too much.
			if len(t) >= 6 && (strings.Contains(n, t) || strings.Contains(t, n)) {
				return 40 + int(40*specificity(term, df))
			}
		}
	}
	return 0
}

// stem strips the suffixes that turn one word into another in the two languages the vault writes in.
// It is deliberately small: a stemmer that is too eager is worse than none, because it makes
// different words look alike.
func stem(word string) string {
	w := word
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		w = w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "sses") && len(w) > 5:
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "es") && len(w) > 4:
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 3:
		w = w[:len(w)-1]
	}
	switch {
	case strings.HasSuffix(w, "ing") && len(w) > 5:
		return w[:len(w)-3]
	case strings.HasSuffix(w, "ung") && len(w) > 5:
		return w[:len(w)-3]
	case strings.HasSuffix(w, "ed") && len(w) > 4:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "en") && len(w) > 5:
		return w[:len(w)-2]
	}
	return w
}

// levenshtein is the edit distance, capped at 3: an exact number above that never changes a match.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > 2 {
		return 3
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	if prev[len(rb)] > 3 {
		return 3
	}
	return prev[len(rb)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// koelner is the Cologne Phonetic code (Kölner Phonetik): words that SOUND alike share it. German
// names are its home ground ("Müller"/"Miller"), and it also helps with the typos people make in
// borrowed words.
func koelner(word string) string {
	s := strings.ToUpper(strings.NewReplacer("Ä", "A", "Ö", "O", "Ü", "U", "ß", "S").Replace(word))
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		prev := byte(0)
		if i > 0 {
			prev = s[i-1]
		}
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}
		code := byte(0)
		switch c {
		case 'A', 'E', 'I', 'J', 'O', 'U', 'Y':
			code = '0'
		case 'H':
			continue
		case 'B':
			code = '1'
		case 'P':
			code = '1'
			if next == 'H' {
				code = '3'
			}
		case 'D', 'T':
			code = '2'
			if next == 'C' || next == 'S' || next == 'Z' {
				code = '8'
			}
		case 'F', 'V', 'W':
			code = '3'
		case 'G', 'K', 'Q':
			code = '4'
		case 'C':
			switch {
			case i == 0 && strings.ContainsRune("AHKLOQRUX", rune(next)):
				code = '4'
			case prev == 'S' || prev == 'Z':
				code = '8'
			case strings.ContainsRune("AHKOQUX", rune(next)):
				code = '4'
			default:
				code = '8'
			}
		case 'X':
			if prev == 'C' || prev == 'K' || prev == 'Q' {
				code = '8'
			} else {
				code = '4'
			}
		case 'L':
			code = '5'
		case 'M', 'N':
			code = '6'
		case 'R':
			code = '7'
		case 'S', 'Z':
			code = '8'
		}
		if code == 0 {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == code {
			continue
		}
		out = append(out, code)
	}
	if len(out) == 0 {
		return ""
	}
	res := []byte{out[0]}
	for _, c := range out[1:] {
		if c != '0' {
			res = append(res, c)
		}
	}
	return string(res)
}

// specificity is how much a term narrows the vocabulary: the average inverse document frequency of
// its words, 0..1. A term made of words every name contains says nothing.
func specificity(term []string, df map[string]int) float64 {
	if len(term) == 0 {
		return 0
	}
	total, n := 0.0, 0
	for _, w := range term {
		if d, ok := df[w]; ok && d > 0 {
			total += 1.0 / float64(d)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return total / float64(n)
}

func subset(a, b []string) bool {
	set := map[string]bool{}
	for _, w := range b {
		set[w] = true
	}
	for _, w := range a {
		if !set[w] {
			return false
		}
	}
	return true
}

func dedupeTerms(terms []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// meaningfulWords is what a request is about, without the words that build the sentence.
func meaningfulWords(text string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, word := range strings.Fields(strings.ToLower(text)) {
		word = strings.Trim(word, ".,;:!?()[]{}'\"„“”")
		if len(word) < 3 || seen[word] {
			continue
		}
		if commandStopwords[word] || lensWordSet[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}

// mappedNames pairs each matched name with the vault's count and what it contributed here.
func mappedNames(m mapping, found map[string]int) []entities.CommandName {
	out := make([]entities.CommandName, 0, len(m.Tags)+len(m.Topics))
	// Only the names that actually produced items. A name that matched the words but contributed
	// nothing is noise above a list that is already the answer.
	for _, t := range m.Tags {
		if n := found["tag:"+t.Name]; n > 0 {
			out = append(out, entities.CommandName{Name: t.Name, Kind: "tag", Count: t.Count, Found: n})
		}
	}
	for _, t := range m.Topics {
		if n := found["topic:"+t.Name]; n > 0 {
			out = append(out, entities.CommandName{Name: t.Name, Kind: "topic", Count: t.Count, Found: n})
		}
	}
	return out
}

// explain writes the mapping the reader can check: "software-architecture (67 sources)".
func (m mapping) explain() []string {
	var out []string
	if len(m.Tags) > 0 {
		parts := make([]string, 0, len(m.Tags))
		for _, t := range m.Tags {
			parts = append(parts, fmt.Sprintf("%s (%d)", t.Name, t.Count))
		}
		out = append(out, "tags "+strings.Join(parts, ", "))
	}
	if len(m.Topics) > 0 {
		parts := make([]string, 0, len(m.Topics))
		for _, t := range m.Topics {
			parts = append(parts, fmt.Sprintf("%s (%d)", t.Name, t.Count))
		}
		out = append(out, "topics "+strings.Join(parts, ", "))
	}
	return out
}

func mappingRationale(m mapping, total int) string {
	names := make([]string, 0, len(m.Tags)+len(m.Topics))
	for _, t := range m.Tags {
		names = append(names, t.Name)
	}
	for _, t := range m.Topics {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return fmt.Sprintf("%d item(s)", total)
	}
	return fmt.Sprintf("%d item(s) under %s", total, strings.Join(names, ", "))
}
