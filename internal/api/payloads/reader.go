package payloads

import (
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ReaderState is GET /api/v1/reader: the reader's two seen watermarks. An empty value means that
// scope has not been acknowledged yet, not that nothing has happened.
type ReaderState struct {
	StartSeenAt   string `json:"start_seen_at,omitempty"`
	SourcesSeenAt string `json:"sources_seen_at,omitempty"`
}

// ReaderActivation is POST /api/v1/reader/active. Scope names the watermark that was acknowledged.
// Since is the baseline that scope marks from; it is empty on the first activation, when nothing is
// new there. SeenAt is the watermark after the acknowledgment. First says the scope was initialized
// by this call.
type ReaderActivation struct {
	Scope  string `json:"scope"`
	Since  string `json:"since,omitempty"`
	SeenAt string `json:"seen_at"`
	First  bool   `json:"first,omitempty"`
}

// ReaderStateFromUseCase maps both watermarks to their wire shape.
func ReaderStateFromUseCase(st entities.ReaderState) ReaderState {
	return ReaderState{
		StartSeenAt:   readerInstant(st.StartSeenAt),
		SourcesSeenAt: readerInstant(st.SourcesSeenAt),
	}
}

// ReaderActivationFromUseCase maps one acknowledgment to its wire shape.
func ReaderActivationFromUseCase(act entities.ReaderActivation) ReaderActivation {
	out := ReaderActivation{
		Scope:  string(act.Scope),
		SeenAt: readerInstant(act.SeenAt),
		First:  act.First,
	}
	if !act.First {
		out.Since = readerInstant(act.Since)
	}
	return out
}

// readerInstant keeps the sub-second part: the watermark is a source's activity instant (its arrival
// or the time its content last changed), and rounding it to the second would make something that
// happened in the same second look older than it is.
func readerInstant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
