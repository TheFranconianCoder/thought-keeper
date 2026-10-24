package interfaces

import "context"

// SearchHit is one lexical search result (a claim or a source).
type SearchHit struct {
	ID      string
	Kind    string // "atom" | "source"
	Title   string
	Snippet string
}

// SearchRepository is lexical (FTS5, or LIKE when degraded) search over claims and source outlines.
// Pages are not searched: there are none.
type SearchRepository interface {
	Search(ctx context.Context, query string, layer string, limit int) ([]SearchHit, error)
}
