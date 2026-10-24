package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// PartStateValue says how far the distilling of one PART of a source has come.
type PartStateValue string

const (
	// PartDone: the agent finished this part on its own terms.
	PartDone PartStateValue = "done"
	// PartIncomplete: the run stopped mid-part (iteration cap, timeout, error). The part has some
	// atoms but is NOT finished and must be derived again on the next run — "has atoms" is not the
	// same as "done".
	PartIncomplete PartStateValue = "incomplete"
)

// PartState is the record of one part's distilling.
type PartState struct {
	NodeID    string
	State     PartStateValue
	Reason    string // why it is incomplete: max_iterations, error, aborted, …
	Atoms     int
	RunID     ulid.ULID
	UpdatedAt time.Time
}

// Finished reports whether the part may be skipped on a resume.
func (s PartState) Finished() bool { return s.State == PartDone }
