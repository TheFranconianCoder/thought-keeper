package partialentities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// PartialAsk is the DTO for one persisted question/answer.
type PartialAsk struct {
	ID        ulid.ULID
	Question  string
	Answer    string
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}
