package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// AtomType classifies a claim.
type AtomType string

const (
	AtomFact         AtomType = "fact"
	AtomDefinition   AtomType = "definition"
	AtomClaim        AtomType = "claim"
	AtomExample      AtomType = "example"
	AtomCounterpoint AtomType = "counterpoint"
	AtomQuote        AtomType = "quote"
)

// AtomStatus makes stale/contradiction concrete.
type AtomStatus string

const (
	AtomActive     AtomStatus = "active"
	AtomSuperseded AtomStatus = "superseded"
	AtomDisputed   AtomStatus = "disputed"
)

// AtomSource is provenance: the source node a claim came from.
type AtomSource struct {
	Hash   string
	Title  string // the source's document title: the name a reader sees, not its hash
	NodeID string
	OCR    bool
}

// Atom is one claim: the smallest unit of knowledge (the Zettelkasten).
type Atom struct {
	ID          ulid.ULID
	Topic       string
	Path        string
	Claim       string
	Body        string
	Type        AtomType
	Status      AtomStatus
	Sources     []AtomSource
	SourceCount int
	Links       []Link
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Frontmatter string // raw YAML map, round-tripped on write
}

// TopicUse is a claim topic with its atom count.
type TopicUse struct {
	Topic string
	Count int
}
