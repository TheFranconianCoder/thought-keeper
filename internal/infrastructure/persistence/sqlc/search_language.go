package sqlc

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
)

// Languages returns the language hints present in the vault. The SET, not a single language, is what
// the expansion uses: a vault may hold German and English sources, and a question may ask in either.
func (r *SearchRepository) Languages(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT meta FROM sources WHERE meta <> ''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m struct {
			Language string `json:"language"`
		}
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		lang := strings.ToLower(strings.TrimSpace(m.Language))
		if lang == "" || lang == "und" || seen[lang] {
			continue
		}
		seen[lang] = true
		out = append(out, lang)
	}
	sort.Strings(out)
	return out, rows.Err()
}

// variantGroups turns each query word into a group of terms: the word itself, the roots of common
// suffixes, and a short prefix family. Only the final FTS query combines the groups; exact search is
// always tried first, so these are a recall fallback, not a replacement.
//
// The rules live in core/textmatch because the interest matcher uses the same word families; this
// wrapper only keeps the FTS query builder's shape.
func variantGroups(words []string, languages []string) [][]string {
	if len(words) == 0 {
		return nil
	}
	out := make([][]string, 0, len(words))
	for _, word := range words {
		out = append(out, textmatch.Variants(word, languages))
	}
	return out
}

// ftsVariantQuery builds grouped FTS syntax: (word OR root* OR prefix*) AND (...). The terms are
// lowercased, quoted for FTS safety, and a trailing '*' becomes a prefix operator.
func ftsVariantQuery(groups [][]string, joiner string) string {
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		terms := make([]string, 0, len(group))
		for _, term := range group {
			prefix := strings.HasSuffix(term, "*")
			term = strings.TrimSuffix(term, "*")
			term = strings.ReplaceAll(term, `"`, `""`)
			if term == "" {
				continue
			}
			if prefix {
				terms = append(terms, `"`+term+`"*`)
			} else {
				terms = append(terms, `"`+term+`"`)
			}
		}
		if len(terms) > 0 {
			parts = append(parts, "("+strings.Join(terms, " OR ")+")")
		}
	}
	return strings.Join(parts, joiner)
}
