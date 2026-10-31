package sqlc

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/interests"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// InterestRepository is the preview's read over the whole vault: which sources carry the terms in
// their topics, their claim texts, their title or their labels.
//
// The INGEST path does not come through here. A new source is matched in memory (it already has its
// claims), and this repository exists for the one moment a rule is created or edited and the reader
// wants to see what it would catch. That is why it may scan the source table: the calibration is
// explicit and rare, not a per-source cost.
type InterestRepository struct {
	db  *sql.DB
	fts bool
}

var _ interfaces.InterestSignalReader = (*InterestRepository)(nil)

// NewInterestRepository wraps a database handle. fts says whether the build has FTS5; without it
// the claim-text signal falls back to a substring scan over the term.
func NewInterestRepository(db *sql.DB, fts bool) *InterestRepository {
	return &InterestRepository{db: db, fts: fts}
}

// contentOnly keeps sitemap/feed CONTAINERS out of the match set: they are lists of sources, not
// documents, and their pages are matched individually.
const contentOnly = `
  AND (CASE WHEN json_valid(meta) THEN COALESCE(CAST(json_extract(meta, '$.sitemap') AS TEXT), '') ELSE '' END) NOT IN ('true', '1')
  AND (CASE WHEN json_valid(meta) THEN COALESCE(CAST(json_extract(meta, '$.rss') AS TEXT), '') ELSE '' END) NOT IN ('true', '1')`

// Candidates returns every source that carries at least one of the terms, with the per-term
// evidence. Excluded terms are passed in by the caller, because a veto has to be measured too.
func (r *InterestRepository) Candidates(ctx context.Context, terms []string) ([]interfaces.InterestCandidate, error) {
	prepared := prepareInterestTerms(terms)
	if len(prepared) == 0 {
		return nil, nil
	}
	c := newCollector()
	if err := r.metaSignals(ctx, prepared, c); err != nil {
		return nil, err
	}
	if err := r.topicSignals(ctx, prepared, c); err != nil {
		return nil, err
	}
	for _, p := range prepared {
		if err := r.claimSignals(ctx, p, c); err != nil {
			return nil, err
		}
	}
	out := make([]interfaces.InterestCandidate, 0, len(c.byHash))
	for _, candidate := range c.byHash {
		if len(candidate.Signals) == 0 {
			continue
		}
		out = append(out, *candidate)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out, nil
}

// collector accumulates candidates lazily: a source enters the map only when a term actually found
// something, so a preview does not carry an empty candidate per source and term.
type collector struct {
	byHash map[string]*interfaces.InterestCandidate
}

func newCollector() *collector {
	return &collector{byHash: map[string]*interfaces.InterestCandidate{}}
}

func (c *collector) get(hash, title string, kind entities.SourceKind) *interfaces.InterestCandidate {
	candidate, ok := c.byHash[hash]
	if !ok {
		candidate = &interfaces.InterestCandidate{Hash: hash, Title: title, Kind: kind, Signals: interests.Signals{}}
		c.byHash[hash] = candidate
	}
	if candidate.Title == "" {
		candidate.Title = title
	}
	return candidate
}

type preparedInterest struct {
	raw  string
	term textmatch.Term
}

func prepareInterestTerms(terms []string) []preparedInterest {
	out := make([]preparedInterest, 0, len(terms))
	seen := map[string]bool{}
	for _, raw := range terms {
		raw = strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
		if raw == "" || seen[textmatch.Normalize(raw)] {
			continue
		}
		term := textmatch.NewTerm(raw)
		if term.Empty() {
			continue
		}
		seen[textmatch.Normalize(raw)] = true
		out = append(out, preparedInterest{raw: raw, term: term})
	}
	return out
}

// metaSignals reads the title and labels of every content source in pages. It is the only signal
// that has no SQL equivalent: the comparison is the same folded word family the ingest path uses.
func (r *InterestRepository) metaSignals(ctx context.Context, terms []preparedInterest, c *collector) error {
	const page = 2000
	for offset := 0; ; offset += page {
		rows, err := r.db.QueryContext(ctx, `
			SELECT hash, title, kind, tags, auto_tags FROM sources
			WHERE 1 = 1`+contentOnly+`
			ORDER BY hash LIMIT ? OFFSET ?`, page, offset)
		if err != nil {
			return fmt.Errorf("interest preview meta: %w", err)
		}
		found := 0
		for rows.Next() {
			found++
			var hash, title, kind, tags, autoTags string
			if err := rows.Scan(&hash, &title, &kind, &tags, &autoTags); err != nil {
				_ = rows.Close()
				return err
			}
			labels := append(decodeSourceTags(tags), decodeSourceTags(autoTags)...)
			for _, p := range terms {
				sig := interests.TermSignal{}
				if p.term.InText(title) {
					sig.Title = true
				}
				for _, label := range labels {
					if p.term.InText(label) {
						sig.Tags = append(sig.Tags, label)
					}
				}
				if sig.Empty() {
					continue
				}
				c.get(hash, title, entities.SourceKind(kind)).Signals[p.raw] = sig
			}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if found < page {
			return nil
		}
	}
}

func (r *InterestRepository) topicSignals(ctx context.Context, terms []preparedInterest, c *collector) error {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT topic FROM atoms`)
	if err != nil {
		return fmt.Errorf("interest preview topics: %w", err)
	}
	var topics []string
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			_ = rows.Close()
			return err
		}
		topics = append(topics, topic)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(topics) == 0 {
		return nil
	}
	for _, p := range terms {
		matched := make([]string, 0, 4)
		for _, topic := range topics {
			if p.term.InTopic(topic) {
				matched = append(matched, topic)
			}
		}
		for start := 0; start < len(matched); start += 100 {
			end := min(start+100, len(matched))
			if err := r.topicProvenance(ctx, p.raw, matched[start:end], c); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *InterestRepository) topicProvenance(ctx context.Context, term string, topics []string, c *collector) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(topics)), ",")
	args := make([]any, 0, len(topics))
	for _, topic := range topics {
		args = append(args, topic)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT src.hash, src.title, src.kind, a.topic, count(DISTINCT a.id)
		FROM atoms a
		JOIN atom_sources s ON s.atom_id = a.id
		JOIN sources src ON src.id = s.source_id
		WHERE a.topic IN (`+placeholders+`)`+contentOnly+`
		GROUP BY src.hash, a.topic`, args...)
	if err != nil {
		return fmt.Errorf("interest preview topic provenance: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var hash, title, kind, topic string
		var n int
		if err := rows.Scan(&hash, &title, &kind, &topic, &n); err != nil {
			return err
		}
		signals := c.get(hash, title, entities.SourceKind(kind)).Signals
		sig := signals[term]
		sig.TopicClaims += n
		if !containsString(sig.Topics, topic) {
			sig.Topics = append(sig.Topics, topic)
		}
		signals[term] = sig
	}
	return rows.Err()
}

func (r *InterestRepository) claimSignals(ctx context.Context, p preparedInterest, c *collector) error {
	if r.fts {
		expr := interestFTSExpr(p.term)
		if expr == "" {
			return nil
		}
		rows, err := r.db.QueryContext(ctx, `
			SELECT src.hash, src.title, src.kind, count(DISTINCT a.id)
			FROM atoms_fts
			JOIN atoms a ON a.rowid = atoms_fts.rowid
			JOIN atom_sources s ON s.atom_id = a.id
			JOIN sources src ON src.id = s.source_id
			WHERE atoms_fts MATCH ?`+contentOnly+`
			GROUP BY src.hash`, expr)
		if err != nil {
			return fmt.Errorf("interest preview claims: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var hash, title, kind string
			var n int
			if err := rows.Scan(&hash, &title, &kind, &n); err != nil {
				return err
			}
			signals := c.get(hash, title, entities.SourceKind(kind)).Signals
			sig := signals[p.raw]
			sig.Claims = n
			signals[p.raw] = sig
		}
		return rows.Err()
	}
	// Degraded search: no FTS5, so the claim-text signal is a substring scan. It is exact for the
	// literal term and deliberately not a claim about word families.
	pattern := strings.ToLower(p.raw)
	rows, err := r.db.QueryContext(ctx, `
		SELECT src.hash, src.title, src.kind, count(DISTINCT a.id)
		FROM atoms a
		JOIN atom_sources s ON s.atom_id = a.id
		JOIN sources src ON src.id = s.source_id
		WHERE (instr(lower(a.claim), ?) > 0 OR instr(lower(a.body), ?) > 0)`+contentOnly+`
		GROUP BY src.hash`, pattern, pattern)
	if err != nil {
		return fmt.Errorf("interest preview claims (degraded): %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var hash, title, kind string
		var n int
		if err := rows.Scan(&hash, &title, &kind, &n); err != nil {
			return err
		}
		signals := c.get(hash, title, entities.SourceKind(kind)).Signals
		sig := signals[p.raw]
		sig.Claims = n
		signals[p.raw] = sig
	}
	return rows.Err()
}

// interestFTSExpr builds the prefix query that mirrors textmatch.WordsMatch: the exact word, the
// word as a prefix when it is long enough to be a derivation ("async" → "asynchronous"), and every
// suffix-stripped root ("cache" → "cach*"). A multi-word term joins its groups with AND.
func interestFTSExpr(term textmatch.Term) string {
	groups := make([]string, 0, len(term.Words))
	for _, word := range term.Words {
		parts := make([]string, 0, len(word.Roots)+1)
		seen := map[string]bool{}
		add := func(value string, prefix bool) {
			value = strings.ReplaceAll(value, `"`, `""`)
			if value == "" {
				return
			}
			key := value
			if prefix {
				key += "*"
			}
			if seen[key] {
				return
			}
			seen[key] = true
			if prefix {
				parts = append(parts, `"`+value+`"*`)
			} else {
				parts = append(parts, `"`+value+`"`)
			}
		}
		add(word.Word, false)
		if len([]rune(word.Word)) >= 5 {
			add(word.Word, true)
		}
		for root := range word.Roots {
			if root != word.Word {
				add(root, true)
			}
		}
		sort.Strings(parts)
		if len(parts) > 0 {
			groups = append(groups, "("+strings.Join(parts, " OR ")+")")
		}
	}
	return strings.Join(groups, " AND ")
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
