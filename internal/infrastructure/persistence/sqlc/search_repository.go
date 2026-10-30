package sqlc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// SearchRepository is lexical search: FTS5 when available, LIKE when degraded.
// It uses raw SQL because sqlc does not model FTS5 MATCH/snippet.
type SearchRepository struct {
	db  *sql.DB
	fts bool
}

var _ interfaces.SearchRepository = (*SearchRepository)(nil)

// NewSearchRepository wraps a database handle.
func NewSearchRepository(db *sql.DB, fts bool) *SearchRepository {
	return &SearchRepository{db: db, fts: fts}
}

// Search runs a query over claims and/or source outlines.
func (r *SearchRepository) Search(ctx context.Context, query, layer string, limit int) ([]interfaces.SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	languages, _ := r.Languages(ctx)
	var hits []interfaces.SearchHit
	if layer == "" || layer == "atoms" {
		h, err := r.searchTable(ctx, "atoms_fts", "atoms", "claim", q, limit, "atom", languages, "")
		if err != nil {
			return nil, err
		}
		hits = append(hits, h...)
	}
	if layer == "" || layer == "sources" {
		h, err := r.searchSources(ctx, q, limit, languages)
		if err != nil {
			return nil, err
		}
		hits = append(hits, h...)
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// SearchInSource searches the claims of ONE source: the same lexical ladder as Search, restricted
// by provenance. A scoped Ask must never see another source's claims.
func (r *SearchRepository) SearchInSource(ctx context.Context, query, sourceHash string, limit int) ([]interfaces.SearchHit, error) {
	q := strings.TrimSpace(query)
	hash := strings.TrimSpace(sourceHash)
	if q == "" || hash == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	languages, _ := r.Languages(ctx)
	return r.searchTable(ctx, "atoms_fts", "atoms", "claim", q, limit, "atom", languages, hash)
}

// searchSources finds sources by their outline node titles/summaries — this is what
// makes bookmarks (which have one outline node) findable.
func (r *SearchRepository) searchSources(ctx context.Context, q string, limit int, languages []string) ([]interfaces.SearchHit, error) {
	if r.fts {
		words, _ := queryParts(q)
		ftsQ := ftsQuery(words, " OR ")
		if ftsQ != "" {
			query := `SELECT src.hash, n.title, snippet(source_nodes_fts, 0, '[', ']', '…', 12)
			          FROM source_nodes_fts
			          JOIN source_nodes n ON n.rowid = source_nodes_fts.rowid
			          JOIN sources src ON src.id = n.source_id
			          WHERE source_nodes_fts MATCH ?
			          ORDER BY rank LIMIT ?`
			rows, err := r.db.QueryContext(ctx, query, ftsQ, limit)
			if err == nil {
				hits, serr := scanSourceHits(rows)
				_ = rows.Close()
				if serr == nil && len(hits) > 0 {
					return hits, nil
				}
				// An EMPTY FTS result is not proof of no match: an index written before the triggers
				// existed has no rows. Fall through to LIKE rather than reporting nothing.
			}
		}
		// A title may use another inflection or derivation ("Chaos" vs "chaotisch"). Expand only
		// after the exact FTS query came back empty: precision first, recall second.
		if groups := variantGroups(words, languages); len(groups) > 0 {
			vq := ftsVariantQuery(groups, " OR ")
			if vq != "" {
				query := `SELECT src.hash, n.title, snippet(source_nodes_fts, 0, '[', ']', '…', 12)
				          FROM source_nodes_fts
				          JOIN source_nodes n ON n.rowid = source_nodes_fts.rowid
				          JOIN sources src ON src.id = n.source_id
				          WHERE source_nodes_fts MATCH ?
				          ORDER BY rank LIMIT ?`
				if rows, err := r.db.QueryContext(ctx, query, vq, limit); err == nil {
					vh, serr := scanSourceHits(rows)
					_ = rows.Close()
					if serr == nil && len(vh) > 0 {
						return vh, nil
					}
				}
			}
		}
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT hash, title, substr(title, 1, 160) FROM sources WHERE title LIKE ? LIMIT ?`,
		"%"+q+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("search sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSourceHits(rows)
}

func scanSourceHits(rows *sql.Rows) ([]interfaces.SearchHit, error) {
	var out []interfaces.SearchHit
	for rows.Next() {
		var hash, title, snippet string
		if err := rows.Scan(&hash, &title, &snippet); err != nil {
			return nil, err
		}
		out = append(out, interfaces.SearchHit{ID: hash, Kind: "source", Title: title, Snippet: snippet})
	}
	return out, rows.Err()
}

func (r *SearchRepository) searchTable(ctx context.Context, ftsTable, contentTable, titleCol, q string, limit int, kind string, languages []string, scope string) ([]interfaces.SearchHit, error) {
	words, symbols := queryParts(q)
	var out []interfaces.SearchHit
	seen := map[string]bool{}
	add := func(hits []interfaces.SearchHit) {
		for _, h := range hits {
			if len(out) >= limit {
				return
			}
			if seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			out = append(out, h)
		}
	}

	// A term such as C++, C# or .NET is not an FTS5 expression: `+` is a syntax error, and the
	// tokenizer would reduce C++ to a single `c`. Search those terms exactly on claim/body first.
	if len(symbols) > 0 {
		combo := append(append([]string(nil), symbols...), words...)
		hits, err := r.likeTerms(ctx, contentTable, titleCol, combo, limit, true, scope)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			// Requiring every word can be too strict (a C++ claim need not literally contain
			// "language" and "complexity"); fall back to the symbol itself.
			hits, err = r.likeTerms(ctx, contentTable, titleCol, symbols, limit, false, scope)
			if err != nil {
				return nil, err
			}
		}
		add(hits)
	}

	if len(words) > 0 && len(out) < limit {
		// FTS5 implicit AND first (precision), then OR if that yields nothing (recall). Quoting
		// every term makes user punctuation (quotes, parentheses, colons) harmless.
		for _, joiner := range []string{" AND ", " OR "} {
			query := ftsQuery(words, joiner)
			hits, err := r.ftsSearch(ctx, ftsTable, contentTable, titleCol, query, limit, kind, scope)
			if err != nil {
				continue
			}
			if len(hits) > 0 {
				add(hits)
				break
			}
		}
	}

	// Morphological recall: exact terms already run first above. Only now expand each word into its
	// stem/prefix family, so "Chaos" also finds "chaotisch" and "caching" also finds "cache" without
	// turning every search into a fuzzy one.
	if len(words) > 0 && len(out) < limit {
		groups := variantGroups(words, languages)
		for _, joiner := range []string{" AND ", " OR "} {
			query := ftsVariantQuery(groups, joiner)
			if query == "" {
				continue
			}
			hits, err := r.ftsSearch(ctx, ftsTable, contentTable, titleCol, query, limit, kind, scope)
			if err != nil {
				continue
			}
			if len(hits) > 0 {
				add(hits)
				break
			}
		}
	}

	// Last resort: the raw phrase, then any of its terms. This is what keeps a query with an
	// unusual shape from returning an empty result while the claims are right there.
	if len(out) == 0 {
		hits, err := r.likeSearch(ctx, contentTable, titleCol, q, limit, kind, scope)
		if err != nil {
			return nil, err
		}
		add(hits)
	}
	return out, nil
}

// queryParts splits a free-text query into FTS-safe words and symbol-bearing terms. Symbols are
// preserved (`C++`, `C#`, `.NET`, `F#`, `node.js`), because FTS5 cannot express them and the
// agent's questions are full of them.
func queryParts(q string) (words, symbols []string) {
	seenWord := map[string]bool{}
	seenSymbol := map[string]bool{}
	fields := strings.FieldsFunc(q, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', ';', ':', '(', ')', '[', ']', '{', '}', '"', '\'', '?', '!':
			return true
		}
		return false
	})
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?()[]{}<>\"'")
		if f == "" {
			continue
		}
		hasLetter := false
		for _, r := range f {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				hasLetter = true
				break
			}
		}
		if !hasLetter {
			continue
		}
		if strings.ContainsAny(f, "+#.@/_") {
			if !seenSymbol[f] {
				seenSymbol[f] = true
				symbols = append(symbols, f)
			}
			continue
		}
		f = strings.ReplaceAll(f, "-", " ")
		for w := range strings.FieldsSeq(f) {
			w = strings.ToLower(w)
			if w == "" || seenWord[w] {
				continue
			}
			seenWord[w] = true
			words = append(words, w)
		}
	}
	return words, symbols
}

// ftsQuery quotes every term so FTS5 operators in user input cannot produce a syntax error.
func ftsQuery(words []string, joiner string) string {
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ReplaceAll(w, `"`, `""`)
		quoted = append(quoted, `"`+w+`"`)
	}
	return strings.Join(quoted, joiner)
}

// likeTerms searches claim/body for terms. With all=true every term must occur; with all=false
// any one is enough. LIKE wildcards in the user's term are escaped. A scope restricts the search to
// the claims whose provenance points at that source.
func (r *SearchRepository) likeTerms(ctx context.Context, table, titleCol string, terms []string, limit int, all bool, scope string) ([]interfaces.SearchHit, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	prefix := ""
	if scope != "" {
		prefix = "a."
	}
	conds := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)*2+1)
	for _, term := range terms {
		pat := "%" + escapeLike(term) + "%"
		conds = append(conds, fmt.Sprintf("(%s%s LIKE ? ESCAPE '\\' OR %sbody LIKE ? ESCAPE '\\')", prefix, titleCol, prefix))
		args = append(args, pat, pat)
	}
	join := " OR "
	if all {
		join = " AND "
	}
	var query string
	if scope != "" {
		query = fmt.Sprintf(`SELECT a.id, a.%s, substr(a.body, 1, 160)
			FROM atoms a
			WHERE a.id IN (
				SELECT s.atom_id FROM atom_sources s
				JOIN sources src ON src.id = s.source_id
				WHERE src.hash = ?
			) AND %s LIMIT ?`, titleCol, strings.Join(conds, join))
		args = append([]any{scope}, args...)
	} else {
		query = fmt.Sprintf("SELECT id, %s, substr(body, 1, 160) FROM %s WHERE %s LIMIT ?", titleCol, table, strings.Join(conds, join))
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	return scanHits(rows, "atom")
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (r *SearchRepository) ftsSearch(ctx context.Context, ftsTable, contentTable, titleCol, q string, limit int, kind string, scope string) ([]interfaces.SearchHit, error) {
	var (
		query string
		args  []any
	)
	if scope != "" {
		query = fmt.Sprintf(
			`SELECT a.id, a.%s, snippet(%s, 1, '[', ']', '…', 12)
			 FROM %s JOIN atoms a ON a.rowid = %s.rowid
			 WHERE %s MATCH ? AND a.id IN (
				SELECT s.atom_id FROM atom_sources s
				JOIN sources src ON src.id = s.source_id
				WHERE src.hash = ?
			 ) ORDER BY rank LIMIT ?`,
			titleCol, ftsTable, ftsTable, ftsTable, ftsTable)
		args = []any{q, scope, limit}
	} else {
		query = fmt.Sprintf(
			`SELECT c.id, c.%s, snippet(%s, 1, '[', ']', '…', 12)
			 FROM %s JOIN %s c ON c.rowid = %s.rowid
			 WHERE %s MATCH ? ORDER BY rank LIMIT ?`,
			titleCol, ftsTable, ftsTable, contentTable, ftsTable, ftsTable)
		args = []any{q, limit}
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHits(rows, kind)
}

func (r *SearchRepository) likeSearch(ctx context.Context, table, titleCol, q string, limit int, kind string, scope string) ([]interfaces.SearchHit, error) {
	pat := "%" + q + "%"
	var (
		query string
		args  []any
	)
	if scope != "" {
		query = fmt.Sprintf(
			`SELECT a.id, a.%s, substr(a.body, 1, 160) FROM atoms a
			 WHERE a.id IN (
				SELECT s.atom_id FROM atom_sources s
				JOIN sources src ON src.id = s.source_id
				WHERE src.hash = ?
			 ) AND (a.%s LIKE ? OR a.body LIKE ?) LIMIT ?`,
			titleCol, titleCol)
		args = []any{scope, pat, pat, limit}
	} else {
		query = fmt.Sprintf(
			`SELECT id, %s, substr(body, 1, 160) FROM %s WHERE (%s LIKE ? OR body LIKE ?) LIMIT ?`,
			titleCol, table, titleCol)
		args = []any{pat, pat, limit}
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	return scanHits(rows, kind)
}

func scanHits(rows *sql.Rows, kind string) ([]interfaces.SearchHit, error) {
	var out []interfaces.SearchHit
	for rows.Next() {
		var (
			id      []byte
			title   string
			snippet string
		)
		if err := rows.Scan(&id, &title, &snippet); err != nil {
			return nil, err
		}
		out = append(out, interfaces.SearchHit{
			ID:      idFromBytes(id).String(),
			Kind:    kind,
			Title:   title,
			Snippet: snippet,
		})
	}
	return out, rows.Err()
}
