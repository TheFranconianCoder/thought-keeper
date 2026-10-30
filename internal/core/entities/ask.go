package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// Ask is one persisted question and its answer. It is a RECORD of a conversation with the claims,
// not a page: it lives in the vault's asks/ directory and is written automatically when a query
// succeeds. The ID is the query run's ID, so a saved answer can always be traced back to the run.
type Ask struct {
	ID       ulid.ULID
	Question string
	Answer   string
	// Tags are automatic labels derived from the answer's cited claim topics (or, without
	// citations, from the question's meaningful words). They are a label on the saved query list,
	// never a filter on retrieval.
	Tags []string
	// Source is the hash of the one source the question was scoped to, or empty for a vault-wide
	// answer. A scoped answer may only cite that source and its claims.
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
