package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// AskStore persists questions and answers in the vault's asks/ area.
type AskStore interface {
	Write(ctx context.Context, ask entities.Ask) error
	Get(ctx context.Context, id ulid.ULID) (entities.Ask, error)
	List(ctx context.Context, limit, offset int) ([]entities.Ask, error)
	Delete(ctx context.Context, id ulid.ULID) error
	// Normalize rewrites legacy [[claim:...]] citations in saved answers to ordinary Markdown links.
	// It returns the number of files it changed.
	Normalize(ctx context.Context) (int, error)
}
