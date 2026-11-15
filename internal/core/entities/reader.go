package entities

import "time"

// ReaderScope names one of the reader's two independent watermarks. Start's watermark drives the
// statistic the front door shows; Sources' watermark drives the new/updated marks of its rows.
// Visiting one lens never acknowledges the other.
type ReaderScope string

const (
	// ReaderScopeStart is the watermark of the Start lens.
	ReaderScopeStart ReaderScope = "start"
	// ReaderScopeSources is the watermark of the Sources lens.
	ReaderScopeSources ReaderScope = "sources"
)

// Valid reports whether the scope names one of the two watermarks.
func (s ReaderScope) Valid() bool {
	return s == ReaderScopeStart || s == ReaderScopeSources
}

// ReaderState is the reader's view of the vault's activity: two independent instants up to which
// arrivals and page content changes have been seen. It is ONE record per vault, not per client.
// Whichever client becomes active first acknowledges a scope for every client, so the same sources
// are not new on every device. A zero value means that scope has not been acknowledged yet.
type ReaderState struct {
	StartSeenAt   time.Time
	SourcesSeenAt time.Time
}

// ReaderWatermark is one scope's instant, as a visit writes it.
type ReaderWatermark struct {
	Scope  ReaderScope
	SeenAt time.Time
}

// ReaderActivation is the answer to a client that acknowledged one scope. Since is that scope's
// watermark before the acknowledgment — the baseline its marks or its statistic use — and SeenAt is
// the watermark after it. First is true when the scope had no watermark yet: the client marks
// nothing and the record is initialized.
//
// Settled lists every watermark the visit moved, the acknowledged scope included. Opening Sources
// settles Start as well — the list answers the same batch the front door summarizes — so the Start
// statistic clears in every window; opening Start settles Start alone and leaves the Sources marks
// for the reader who actually visits the list. An empty list means the visit changed nothing, and
// no other window is reloaded.
type ReaderActivation struct {
	Scope   ReaderScope
	Since   time.Time
	SeenAt  time.Time
	First   bool
	Settled []ReaderWatermark
}
