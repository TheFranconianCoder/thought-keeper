package payloads

import (
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ReaderState is GET /api/v1/reader: the vault's one seen watermark — the batch of sources the
// reader has not opened the list on yet. An empty value means the vault has not been acknowledged,
// not that nothing has happened.
type ReaderState struct {
	SourcesSeenAt string `json:"sources_seen_at,omitempty"`
}

// ReaderActivation is POST /api/v1/reader/active. Scope echoes the lens that became active. Since is
// the baseline that lens marks from — the watermark before the call — and SeenAt is the watermark
// after it; an empty Since means the first acknowledgment, when nothing is new. Opening the list
// moves the watermark; the front door answers it unchanged, so the statistic and the list's marks
// always describe the same batch.
type ReaderActivation struct {
	Scope  string `json:"scope"`
	Since  string `json:"since,omitempty"`
	SeenAt string `json:"seen_at"`
	First  bool   `json:"first,omitempty"`
}

// ReaderStateFromUseCase maps the watermark to its wire shape.
func ReaderStateFromUseCase(st entities.ReaderState) ReaderState {
	return ReaderState{SourcesSeenAt: readerInstant(st.SourcesSeenAt)}
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
