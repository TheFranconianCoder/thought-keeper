package usecases

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// Asks is the use case of the saved Ask conversations: a record of what was asked, read and deleted,
// never edited.
type Asks struct {
	Store   interfaces.AskStore
	Atoms   interfaces.AtomRepository
	Sources SourceTagReader
}

// AskLister lists the persisted conversations, newest first. A handler holds this value; nil means the
// feature is not wired.
type AskLister func(ctx context.Context, limit, offset int) ([]partialentities.PartialAsk, error)

// List returns the bounded list (the Ask view never loads the whole history at once).
func (uc Asks) List(ctx context.Context, limit, offset int) ([]partialentities.PartialAsk, error) {
	asks, err := uc.Store.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialAsk, 0, len(asks))
	for _, a := range asks {
		tags := a.Tags
		if len(tags) == 0 {
			tags = AskTags(ctx, a.Question, a.Answer, uc.Atoms, uc.Sources)
		}
		out = append(out, partialentities.PartialAsk{
			ID: a.ID, Question: a.Question, Answer: a.Answer, Tags: tags, Source: a.Source,
			CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		})
	}
	return out, nil
}

// AskGetter returns one saved Ask.
type AskGetter func(ctx context.Context, id ulid.ULID) (partialentities.PartialAsk, error)

// Get returns one ask with its answer.
func (uc Asks) Get(ctx context.Context, id ulid.ULID) (partialentities.PartialAsk, error) {
	a, err := uc.Store.Get(ctx, id)
	if err != nil {
		return partialentities.PartialAsk{}, err
	}
	tags := a.Tags
	if len(tags) == 0 {
		tags = AskTags(ctx, a.Question, a.Answer, uc.Atoms, uc.Sources)
	}
	return partialentities.PartialAsk{
		ID: a.ID, Question: a.Question, Answer: a.Answer, Tags: tags, Source: a.Source,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}, nil
}

// AskDeleter deletes one saved Ask.
type AskDeleter func(ctx context.Context, id ulid.ULID) error

// Delete removes one saved conversation (to the vault trash).
func (uc Asks) Delete(ctx context.Context, id ulid.ULID) error {
	return uc.Store.Delete(ctx, id)
}
