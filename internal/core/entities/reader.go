package entities

import "time"

// ReaderScope names the lens a client became active on. Only the Sources list acknowledges the
// vault's one seen watermark; the Start lens is a summary of that same batch and settles nothing.
type ReaderScope string

const (
	// ReaderScopeStart is the Start lens, the front door.
	ReaderScopeStart ReaderScope = "start"
	// ReaderScopeSources is the Sources lens, which owns the acknowledgment.
	ReaderScopeSources ReaderScope = "sources"
)

// Valid reports whether the scope names a lens.
func (s ReaderScope) Valid() bool {
	return s == ReaderScopeStart || s == ReaderScopeSources
}

// ReaderState is the reader's view of the vault's activity: ONE instant up to which arrivals and
// page content changes have been seen. It is one record per vault, not per client, so a source the
// phone has seen is not new on the desktop. A zero value means the vault has not been acknowledged
// yet; it is not "everything is new".
type ReaderState struct {
	SourcesSeenAt time.Time
}

// ReaderActivation is the answer to a client that became active on a lens. Scope echoes the lens.
// Since is the watermark before the visit — the baseline that lens marks from — and SeenAt the
// watermark after it. First is true when the vault had none yet: nothing is new then, and the first
// acknowledgment initializes the record instead of flagging the whole corpus.
//
// Opening the Sources list moves the watermark; the front door answers the current baseline and
// moves nothing, because it only summarizes the batch the list still marks.
type ReaderActivation struct {
	Scope  ReaderScope
	Since  time.Time
	SeenAt time.Time
	First  bool
}
