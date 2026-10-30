package partialentities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// PartialCommandName is one mapped vocabulary name.
type PartialCommandName struct {
	Name  string
	Kind  string
	Count int
	Found int
}

// PartialCommandItem is one member of a command's set, as the wire sees it.
type PartialCommandItem struct {
	Ref   string
	Title string
	Kind  string
	Why   string
}

// PartialCommandCriteria is the interpretation a command was resolved from: what the banner shows
// ("applied: tag cloud · added ≤ 7 days · plant open") and what "run again" re-resolves.
type PartialCommandCriteria struct {
	Text          string
	Tag           string
	Kind          string
	Topic         string
	FromTopic     string
	Tags          []string
	Topics        []string
	Source        string
	Atom          string
	Since         time.Duration
	State         string
	Order         string
	BookmarksOnly bool
	AttachedOnly  bool
}

// PartialCommand is the DTO for one persisted command.
type PartialCommand struct {
	ID         ulid.ULID
	Text       string
	Lens       string
	URL        string
	Resolution string
	Rationale  string
	Notes      []string
	Total      int
	Vault      int
	Scanned    int
	Generated  string
	// Took is the end-to-end time of the request.
	Took      time.Duration
	CreatedAt time.Time
	Criteria  PartialCommandCriteria
	Mapped    []PartialCommandName
	Items     []PartialCommandItem
}
