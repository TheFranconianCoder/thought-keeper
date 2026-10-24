package usecases

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// ListAsks lists the persisted Ask conversations, newest first.
type ListAsks struct {
	Asks  interfaces.AskStore
	Atoms interfaces.AtomRepository
}

// Execute returns the bounded list (the Ask view never loads the whole history at once).
func (uc ListAsks) Execute(ctx context.Context, limit, offset int) ([]partialentities.PartialAsk, error) {
	asks, err := uc.Asks.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialAsk, 0, len(asks))
	for _, a := range asks {
		tags := a.Tags
		if len(tags) == 0 {
			tags = AskTags(ctx, a.Question, a.Answer, uc.Atoms)
		}
		out = append(out, partialentities.PartialAsk{
			ID: a.ID, Question: a.Question, Answer: a.Answer, Tags: tags,
			CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		})
	}
	return out, nil
}

// GetAsk returns one persisted conversation.
type GetAsk struct {
	Asks  interfaces.AskStore
	Atoms interfaces.AtomRepository
}

// Execute returns one ask with its answer.
func (uc GetAsk) Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialAsk, error) {
	a, err := uc.Asks.Get(ctx, id)
	if err != nil {
		return partialentities.PartialAsk{}, err
	}
	tags := a.Tags
	if len(tags) == 0 {
		tags = AskTags(ctx, a.Question, a.Answer, uc.Atoms)
	}
	return partialentities.PartialAsk{
		ID: a.ID, Question: a.Question, Answer: a.Answer, Tags: tags,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}, nil
}

// DeleteAsk removes one saved conversation (to the vault trash).
type DeleteAsk struct {
	Asks interfaces.AskStore
}

// Execute deletes one ask by id.
func (uc DeleteAsk) Execute(ctx context.Context, id ulid.ULID) error {
	return uc.Asks.Delete(ctx, id)
}
