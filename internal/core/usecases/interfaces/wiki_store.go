package interfaces

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// WikiStore reads and writes user-authored wiki pages. The page file is the truth; the source
// representation is derived from it.
type WikiStore interface {
	Read(ctx context.Context, id ulid.ULID) (entities.WikiPage, error)
	Write(ctx context.Context, page entities.WikiPage) error
	List(ctx context.Context) ([]entities.WikiPage, error)
	Remove(ctx context.Context, id ulid.ULID) error
}
