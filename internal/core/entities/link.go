package entities

import (
	"github.com/oklog/ulid/v2"
)

// Link is a wikilink in a body: `[[Title]]`, `[[Title|alias]]`, `[[Title#Heading]]`.
// The edge is the target's id (ToID); the title is only the text the reader sees.
type Link struct {
	// ToID is the identity of the link: the target's id, resolved when the link is
	// written. A title never decides a link — it is only the text the reader sees, and it
	// may change without the edge moving. Zero means "no target" (a link a human typed
	// into a vault file that nothing resolves yet).
	ToID      ulid.ULID
	RawTarget string
	Alias     string
	Heading   string
	Position  int
}
