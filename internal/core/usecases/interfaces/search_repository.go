package interfaces

import "context"

// SearchHit is one lexical search result (a claim or a source).
type SearchHit struct {
	ID      string
	Kind    string // "atom" | "source"
	Title   string
	Snippet string
}

// SearchRepository is lexical (FTS5, or LIKE when degraded) search over claims and the text fields
// of sources. Pages are not searched: there are none.
type SearchRepository interface {
	Search(ctx context.Context, query string, layer string, limit int) ([]SearchHit, error)
	// SearchExact is the strict form: every word must occur, and no OR, variant or raw-phrase
	// fallback is used. It is what a "mentions X" channel needs — a name that is not there must
	// not be approximated into an unrelated claim.
	SearchExact(ctx context.Context, query string, layer string, limit int) ([]SearchHit, error)
}
