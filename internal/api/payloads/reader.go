package payloads

import (
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ReaderState is GET /api/v1/reader: the shared seen watermark. An empty value means no client has
// opened this vault yet, not that nothing has happened.
type ReaderState struct {
	SeenAt string `json:"seen_at,omitempty"`
}

// ReaderActivation is POST /api/v1/reader/active. Since is the baseline the client marks from; it is
// empty on the first activation, when nothing is new. SeenAt is the watermark after the
// acknowledgment. First says the record was initialized by this call.
type ReaderActivation struct {
	Since  string `json:"since,omitempty"`
	SeenAt string `json:"seen_at"`
	First  bool   `json:"first,omitempty"`
}

// ReaderStateFromUseCase maps the watermark to its wire shape.
func ReaderStateFromUseCase(st entities.ReaderState) ReaderState {
	return ReaderState{SeenAt: readerInstant(st.SeenAt)}
}

// ReaderActivationFromUseCase maps one acknowledgment to its wire shape.
func ReaderActivationFromUseCase(act entities.ReaderActivation) ReaderActivation {
	out := ReaderActivation{SeenAt: readerInstant(act.SeenAt), First: act.First}
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
