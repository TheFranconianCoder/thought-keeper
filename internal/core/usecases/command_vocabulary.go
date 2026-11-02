package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

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
	Score int
}

// textEvidence is the lexical channel's contribution: how many claims/titles mentioned the words,
// and how many sources that produced. It is shown beside the names, not instead of them.
type textEvidence struct {
	Hits  int
	Found int
}

// mapping is what a request's words were resolved to: the names, the lexical evidence and the
// terms that found them.
type mapping struct {
	Tags   []nameUses
	Topics []nameUses
	Text   *textEvidence
	// Terms are the words the match ran with — the model's expansion plus the request's own words.
	Terms []string
}

// resolveByVocabulary maps the request onto the vault's names and materializes the set. The words
// stay in Criteria.Text: for a mapped subject they are the lexical channel, not a substring filter.
func (uc Commands) resolveByVocabulary(ctx context.Context, cmd *entities.Command) error {
	// The SUBJECT is the residual text the parser/model left after the lens and the criteria; the
	// utterance still contains "sources about" and "from last week", which are not search terms.
	subject := strings.TrimSpace(cmd.Criteria.Text)
	if subject == "" {
		subject = cmd.Text
	}
	terms, modelFailed := uc.requestTerms(ctx, subject, cmd.Lens)
	if modelFailed {
		cmd.Notes = append(cmd.Notes,
			"the model did not answer; the match ran on the request's own words")
	}
	return uc.resolveVocabularyTerms(ctx, cmd, terms)
}

// resolveVocabularyTerms is the mapping proper, with the terms already decided. The subject path
// gets them from the model; the tag fallback passes the tag's own word, so a missing tag does not
// turn into a second model call and a pile of related concepts.
func (uc Commands) resolveVocabularyTerms(ctx context.Context, cmd *entities.Command, terms []string) error {
	subject := strings.TrimSpace(cmd.Criteria.Text)
	if subject == "" {
		subject = cmd.Text
	}
	requestWords := meaningfulWords(subject)
	requestSet := make(map[string]bool, len(requestWords))
	for _, word := range requestWords {
		requestSet[word] = true
	}
	terms = uc.expandAliases(terms)
	cmd.Criteria.Tags = nil
	cmd.Criteria.Topics = nil
	cmd.Criteria.Terms = terms

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
	vaultSources := 0
	if n, err := uc.Index.CountSources(ctx, entities.SourceFilter{}); err == nil {
		vaultSources = n
	}
	atomCount := 0
	if n, err := uc.Atoms.AtomCount(ctx); err == nil {
		atomCount = n
	}

	mapped := mapping{
		Terms:  terms,
		Tags:   matchNames(terms, tagCounts, requestSet, vaultSources),
		Topics: matchNames(terms, topicCounts, requestSet, atomCount),
	}
	// A claim list is built from topics; a wiki page carries tags, not claim topics; a source list
	// from tags AND the provenance of the topics.
	if cmd.Lens == entities.CommandClaims {
		// A tag says nothing about a claim; only topics do.
		mapped.Tags = nil
	}
	if cmd.Lens == entities.CommandWiki {
		// A wiki page has tags, not claim topics; a topic name is not a page label.
		mapped.Topics = nil
	}

	// The names travel with the record for EVERY lens: they are the reading the banner shows and the
	// reason "run again" is cheap.
	for _, t := range mapped.Tags {
		cmd.Criteria.Tags = append(cmd.Criteria.Tags, t.Name)
	}
	for _, t := range mapped.Topics {
		cmd.Criteria.Topics = append(cmd.Criteria.Topics, t.Name)
	}

	switch cmd.Lens {
	case entities.CommandWiki:
		return uc.resolveWiki(ctx, cmd)
	case entities.CommandClaims:
		return uc.claimsFromMapping(ctx, cmd, mapped)
	default:
		return uc.sourcesFromMapping(ctx, cmd, mapped)
	}
}

// sourcesFromMapping materializes a SOURCE set from the mapped names and the lexical channel: the
// sources tagged with the names, the sources whose claims carry the topics (provenance), and the
// sources whose titles/claims mention the request's words. All are joins, not reads.
func (uc Commands) sourcesFromMapping(ctx context.Context, cmd *entities.Command, mapped mapping) error {
	counts := map[string]int{}
	found := map[string]int{}
	for _, tag := range mapped.Tags {
		srcs, err := uc.Index.ListSources(ctx, entities.SourceFilter{Tag: tag.Name}, uc.scanLimit(), 0)
		if err != nil {
			continue
		}
		for _, s := range srcs {
			counts[s.Hash] += 4 // a tag is a coarser, surer signal than one claim's topic
			found["tag:"+tag.Name]++
		}
		if len(srcs) == uc.scanLimit() {
			cmd.Notes = append(cmd.Notes,
				fmt.Sprintf("read the newest %d sources tagged %q; the tag may have more", len(srcs), tag.Name))
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
		if len(atoms) == uc.scanLimit() {
			cmd.Notes = append(cmd.Notes,
				fmt.Sprintf("read the newest %d claims of topic %q; the topic may have more", len(atoms), topic.Name))
		}
	}
	if cmd.Criteria.Text != "" {
		evidence, lexical := uc.lexicalSourceEvidence(ctx, cmd.Criteria.Text, mapped.Terms, uc.scanLimit())
		if evidence != nil && (evidence.Hits > 0 || evidence.Found > 0) {
			mapped.Text = evidence
		}
		for hash, hits := range lexical {
			counts[hash] += 3 * hits
		}
	}
	if len(counts) == 0 {
		cmd.Total, cmd.Vault, cmd.Scanned = 0, 0, 0
		cmd.Notes = append(cmd.Notes, fmt.Sprintf(
			"no tag, topic, title or claim matches %s", strings.Join(mapped.Terms, ", ")))
		return nil
	}
	cmd.Mapped = mappedNames(mapped, found)
	if mapped.Text != nil {
		cmd.Mapped = append(cmd.Mapped, entities.CommandName{
			Name: cmd.Criteria.Text, Kind: "text", Count: mapped.Text.Hits, Found: mapped.Text.Found,
		})
	}
	sortMapped(cmd.Mapped)
	return uc.resolveSourcesFromCounts(ctx, cmd, counts, true)
}

// lexicalSourceEvidence is the second signal beside the names: the claims that mention the words
// (strict, all words) and the sources whose own title/URL/tag mentions them. The map is hash→hits;
// the caller gives it a weight in the combined score.
func (uc Commands) lexicalSourceEvidence(ctx context.Context, text string, terms []string, limit int) (*textEvidence, map[string]int) {
	if uc.Lexical == nil {
		return nil, nil
	}
	// The model's expansion is useful here too: "DHH" should find the claims that spell the name
	// out, and "LLM" the claims that say "large language models". Every query is still strict.
	queries := dedupeTerms(append([]string{text}, terms...))
	counts := map[string]int{}
	hits := map[string]bool{}
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		if claimHits, err := uc.Lexical.SearchExact(ctx, query, "atoms", limit); err == nil {
			ids := make([]ulid.ULID, 0, len(claimHits))
			for _, hit := range claimHits {
				hits["atom:"+hit.ID] = true
				if id, parseErr := ulid.ParseStrict(hit.ID); parseErr == nil {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				if provenance, pErr := uc.Atoms.SourcesForAtoms(ctx, ids); pErr == nil {
					for _, sources := range provenance {
						for _, src := range sources {
							if src.Hash != "" {
								counts[src.Hash]++
							}
						}
					}
				}
			}
		}
		// A two-letter word is a substring of half the metadata ("ai" in "said"); the claims FTS is
		// the precise channel for it. The title/URL channel needs enough letters to identify one.
		if len([]rune(query)) >= 3 {
			if sourceHits, err := uc.Lexical.SearchExact(ctx, query, "sources", limit); err == nil {
				for _, hit := range sourceHits {
					if hit.ID != "" {
						counts[hit.ID] += 2
						hits["source:"+hit.ID] = true
					}
				}
			}
		}
	}
	return &textEvidence{Hits: len(hits), Found: len(counts)}, counts
}

// claimsFromMapping materializes a CLAIM set from the mapped topics and the lexical channel.
func (uc Commands) claimsFromMapping(ctx context.Context, cmd *entities.Command, mapped mapping) error {
	items := make([]entities.CommandItem, 0, uc.maxItems())
	seen := map[string]bool{}
	found := map[string]int{}
	total := 0
	add := func(id, claim, topic string) {
		if seen[id] {
			return
		}
		seen[id] = true
		total++
		if len(items) < uc.maxItems() {
			items = append(items, entities.CommandItem{Ref: id, Title: claim, Kind: topic})
		}
	}
	for _, topic := range mapped.Topics {
		atoms, err := uc.Atoms.ListAtoms(ctx, topic.Name, uc.scanLimit(), 0)
		if err != nil {
			continue
		}
		for _, a := range atoms {
			if !WindowContains(a.CreatedAt, cmd.Criteria.Window.From, cmd.Criteria.Window.To) {
				continue
			}
			found["topic:"+topic.Name]++
			add(a.ID.String(), a.Claim, a.Topic)
		}
		if len(atoms) == uc.scanLimit() {
			cmd.Notes = append(cmd.Notes,
				fmt.Sprintf("read the newest %d claims of topic %q; the topic may have more", len(atoms), topic.Name))
		}
	}
	if cmd.Criteria.Text != "" && uc.Lexical != nil {
		queries := dedupeTerms(append([]string{cmd.Criteria.Text}, mapped.Terms...))
		hitIDs := map[string]bool{}
		textSeen := map[string]bool{}
		textFound := 0
		for _, query := range queries {
			query = strings.TrimSpace(query)
			if query == "" {
				continue
			}
			hits, err := uc.Lexical.SearchExact(ctx, query, "atoms", uc.scanLimit())
			if err != nil {
				continue
			}
			ids := make([]ulid.ULID, 0, len(hits))
			for _, hit := range hits {
				hitIDs[hit.ID] = true
				if id, parseErr := ulid.ParseStrict(hit.ID); parseErr == nil {
					ids = append(ids, id)
				}
			}
			atoms, aErr := uc.Atoms.AtomTextsForIDs(ctx, ids)
			if aErr != nil {
				continue
			}
			for _, a := range atoms {
				if !WindowContains(a.CreatedAt, cmd.Criteria.Window.From, cmd.Criteria.Window.To) {
					continue
				}
				if !textSeen[a.ID.String()] {
					textSeen[a.ID.String()] = true
					textFound++
				}
				add(a.ID.String(), a.Claim, a.Topic)
			}
		}
		if len(hitIDs) > 0 && textFound > 0 {
			mapped.Text = &textEvidence{Hits: len(hitIDs), Found: textFound}
		}
	}
	cmd.Items = items
	cmd.Total = total
	cmd.Vault, cmd.Scanned = 0, 0
	cmd.Mapped = mappedNames(mapped, found)
	if mapped.Text != nil {
		cmd.Mapped = append(cmd.Mapped, entities.CommandName{
			Name: cmd.Criteria.Text, Kind: "text", Count: mapped.Text.Hits, Found: mapped.Text.Found,
		})
	}
	sortMapped(cmd.Mapped)
	if total == 0 {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf(
			"no topic, title or claim matches %s", strings.Join(mapped.Terms, ", ")))
	}
	return nil
}

// requestTerms produces the words the match runs with: the request's own meaningful words, plus —
// when a model is configured — a small expansion for translation and synonyms.
func (uc Commands) requestTerms(ctx context.Context, text string, lens entities.CommandLens) ([]string, bool) {
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
		// The model is asked to replace the reader's words, not to add related concepts. A term
		// that is sentence glue or a lens word is not a name; "sources" must never become a topic.
		if commandStopwords[t] || commandTimeWords[t] || lensWordSet[t] || isLensWordTypo(t) {
			continue
		}
		terms = append(terms, t)
	}
	return dedupeTerms(terms), false
}

// expandAliases adds the canonical name behind every curated merge. A vocabulary that absorbed
// "cloud-computing" into "cloud" left an alias, and a request that still says the old name must
// still land on the new one — that is the whole point of a curated vocabulary.
func (uc Commands) expandAliases(terms []string) []string {
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
//
// requestWords is the reader's own vocabulary (before the model expanded it); a match that only the
// model's translation supports is downweighted, and a BROAD name is refused unless the reader named
// its words themselves. That is what keeps "LLM" from dragging in "open-source" and "agile" from
// dragging in "agile-development".
func matchNames(terms []string, names map[string]int, requestWords map[string]bool, total int) []nameUses {
	const (
		limit     = 3
		threshold = 70
	)
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
	requestTokens := map[string]bool{}
	for word := range requestWords {
		for _, tok := range splitName(word) {
			requestTokens[tok] = true
		}
	}
	requestTokenList := make([]string, 0, len(requestTokens))
	for tok := range requestTokens {
		requestTokenList = append(requestTokenList, tok)
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
		for _, term := range terms {
			tt := splitName(term)
			s := nameScore(tt, tokens, df)
			if s == 0 {
				continue
			}
			// A model-only term is a translation, not the reader's word: it must work harder. A nil
			// map means "no distinction" (the no-model path and the tests), not "nothing is named".
			if requestWords != nil && !requestWords[strings.Join(tt, " ")] && !requestWords[term] {
				s = s * 3 / 4
			}
			if s > best {
				best = s
			}
		}
		if best < threshold {
			continue
		}
		// A name the vault uses for a large part of itself is a field, not an answer. It survives
		// only when the reader named its words ("open source" is a subject; "open-source" dragged in
		// by an "LLM" expansion is not).
		if total > 0 && count > max(50, total/20) && !subset(tokens, requestTokenList) {
			continue
		}
		out = append(out, scored{name: name, count: count, score: best})
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
	// Keep the names that are close to the strongest one: a name at 70% of the best score is a
	// different subject that happens to share a word.
	if len(out) > 1 {
		cutoff := out[0].score * 7 / 10
		kept := out[:0]
		for _, s := range out {
			if s.score >= cutoff {
				kept = append(kept, s)
			}
		}
		out = kept
	}
	res := make([]nameUses, 0, len(out))
	for _, s := range out {
		res = append(res, nameUses{Name: s.name, Count: s.count, Score: s.score})
	}
	return res
}

// nameTokens splits a name into words: "software-architecture" → [software architecture].
func splitName(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ").Replace(s)
	return strings.Fields(s)
}

// nameScore grades how well a term matches a name. Exact beats a full phrase, a full phrase beats a
// partial one; a single shared token in two multi-word names is the weakest kind and usually noise.
func nameScore(term, name []string, df map[string]int) int {
	if len(term) == 0 || len(name) == 0 {
		return 0
	}
	if strings.Join(term, " ") == strings.Join(name, " ") {
		return 200
	}
	// A one-word term that names part of a compound name is a real partial match: "llm" ⊂
	// "llm-capabilities", "software" ⊂ "software-architecture".
	if len(term) == 1 && subset(term, name) {
		return 70 + int(60*specificity(term, df))
	}
	// A one-word NAME that is only one word of a multi-word term is the generic-name trap:
	// "learning" ⊂ "machine-learning", "intelligence-community" vs "artificial-intelligence". It is
	// not a name the reader asked for; keep it far below the threshold.
	if len(name) == 1 && subset(name, term) {
		return 35 + int(20*specificity(term, df))
	}
	if subset(term, name) || subset(name, term) {
		return 70 + int(60*specificity(term, df))
	}
	for _, t := range term {
		for _, n := range name {
			// The same word after a plural or a German suffix: architectures / architecture. When
			// both sides are one word this is a strong match; when one side is compound it is only
			// a shared token.
			if len(t) >= 5 && stem(t) == stem(n) {
				if len(term) == 1 && len(name) == 1 {
					return 95
				}
				coverage := 1.0 / float64(max(len(term), len(name)))
				return 25 + int(25*coverage) + int(20*specificity(term, df))
			}
			// One typo, or two in a long word: kubernetes / kubernetis, architektur / architecture.
			if len(t) >= 5 && len(n) >= 5 {
				if d := levenshtein(t, n); d <= 1 || (d <= 2 && len(t) >= 8) {
					return 75
				}
			}
			// The same sound: Müller / Miller, Schmidt / Schmitt. Gated hard — a phonetic match is
			// the weakest kind and must not turn "architektur" into "architecture".
			if len(t) >= 5 && len(n) >= 5 && t[0] == n[0] && koelner(t) == koelner(n) {
				return 60
			}
			// A long shared word, deliberately conservative: four letters match too much.
			if len(t) >= 6 && (strings.Contains(n, t) || strings.Contains(t, n)) {
				return 50 + int(30*specificity(term, df))
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
	for word := range strings.FieldsSeq(strings.ToLower(text)) {
		word = strings.Trim(word, ".,;:!?()[]{}'\"„“”")
		if len(word) < 3 || seen[word] {
			continue
		}
		if commandStopwords[word] || commandTimeWords[word] || lensWordSet[word] || isLensWordTypo(word) {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}

// sortMapped puts the strongest contribution first. The banner is the explanation; a text row that
// found the person directly should not sit under a broad topic that found more.
func sortMapped(names []entities.CommandName) {
	sort.SliceStable(names, func(i, j int) bool {
		if names[i].Found != names[j].Found {
			return names[i].Found > names[j].Found
		}
		return names[i].Count > names[j].Count
	})
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
	// Strongest contribution first: the banner is the explanation, and the first row should be the
	// signal that produced most of the set.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Found != out[j].Found {
			return out[i].Found > out[j].Found
		}
		return out[i].Count > out[j].Count
	})
	return out
}
