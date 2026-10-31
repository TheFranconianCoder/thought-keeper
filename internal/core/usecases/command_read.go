package usecases

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// CommandLister returns the assistant's history, newest first. A handler holds this value; nil means
// the history is not wired.
type CommandLister func(ctx context.Context, limit, offset int) ([]partialentities.PartialCommand, error)

// List returns the bounded history.
func (uc Commands) List(ctx context.Context, limit, offset int) ([]partialentities.PartialCommand, error) {
	items, err := uc.Store.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]partialentities.PartialCommand, 0, len(items))
	for _, c := range items {
		out = append(out, PartialFromCommand(c))
	}
	return out, nil
}

// CommandGetter returns one command. It is how a lens learns what an address means. A handler holds
// this value; nil means the history is not wired.
type CommandGetter func(ctx context.Context, id ulid.ULID) (partialentities.PartialCommand, error)

// Get returns one command by id.
func (uc Commands) Get(ctx context.Context, id ulid.ULID) (partialentities.PartialCommand, error) {
	c, err := uc.Store.Get(ctx, id)
	if err != nil {
		return partialentities.PartialCommand{}, err
	}
	return PartialFromCommand(c), nil
}

// CommandDeleter removes one command from the history (into the vault trash).
type CommandDeleter func(ctx context.Context, id ulid.ULID) error

// Delete deletes one command.
func (uc Commands) Delete(ctx context.Context, id ulid.ULID) error {
	return uc.Store.Delete(ctx, id)
}

// CommandWindow is what a materialized set knows about itself: how many of its members still
// resolve, and how many have gone since the command ran. The difference is shown, never hidden: a
// deleted source is an honest "1 no longer present", not a shorter list with no explanation.
type CommandWindow struct {
	Total   int
	Missing int
}

// CommandSourceSet resolves an assistant result (?command=<id>) into sources. A handler holds this
// value; nil means the address is not understood here.
type CommandSourceSet func(ctx context.Context, id ulid.ULID, limit, offset int) ([]partialentities.PartialSource, CommandWindow, error)

// Commands resolves an assistant result into sources.
func (uc Sources) Commands(ctx context.Context, id ulid.ULID, limit, offset int) ([]partialentities.PartialSource, CommandWindow, error) {
	cmd, err := uc.CommandStore.Get(ctx, id)
	if err != nil {
		return nil, CommandWindow{}, err
	}
	if cmd.Lens != entities.CommandSources && cmd.Lens != entities.CommandAttention {
		return nil, CommandWindow{}, types.New("conflict", "this command does not address sources", types.ErrConflict)
	}
	out := make([]partialentities.PartialSource, 0, len(cmd.Items))
	window := CommandWindow{}
	for _, item := range cmd.Items {
		src, gErr := uc.Index.GetSourceByHash(ctx, item.Ref)
		if gErr != nil {
			window.Missing++
			continue
		}
		window.Total++
		if window.Total <= offset || len(out) >= limit {
			continue
		}
		out = append(out, partialentities.PartialFromSource(src))
	}
	return out, window, nil
}

// CommandAtomSet resolves an assistant result (?command=<id>) into claims. A handler holds this value;
// nil means the address is not understood here.
type CommandAtomSet func(ctx context.Context, id ulid.ULID, limit, offset int) ([]partialentities.PartialAtom, CommandWindow, error)

// Commands resolves an assistant result into claims.
func (uc Claims) Commands(ctx context.Context, id ulid.ULID, limit, offset int) ([]partialentities.PartialAtom, CommandWindow, error) {
	cmd, err := uc.CommandStore.Get(ctx, id)
	if err != nil {
		return nil, CommandWindow{}, err
	}
	if cmd.Lens != entities.CommandClaims {
		return nil, CommandWindow{}, types.New("conflict", "this command does not address claims", types.ErrConflict)
	}
	out := make([]partialentities.PartialAtom, 0, len(cmd.Items))
	window := CommandWindow{}
	var ids []ulid.ULID
	for _, item := range cmd.Items {
		parsed, pErr := ulid.ParseStrict(item.Ref)
		if pErr != nil {
			window.Missing++
			continue
		}
		atom, gErr := uc.Atoms.GetAtom(ctx, parsed)
		if gErr != nil {
			window.Missing++
			continue
		}
		window.Total++
		if window.Total <= offset || len(out) >= limit {
			continue
		}
		out = append(out, partialentities.PartialFromAtom(atom))
		ids = append(ids, atom.ID)
	}
	// The memberships of the PAGE only, like the ordinary claim list: the row says how many
	// clusters a claim travels in, and a count that is always zero would be a lie.
	if uc.Clusters != nil && len(ids) > 0 {
		if links, lErr := uc.Clusters.MembershipsForAtoms(ctx, ids); lErr == nil {
			membership := map[ulid.ULID][]ulid.ULID{}
			for _, l := range links {
				membership[l.AtomID] = append(membership[l.AtomID], l.ClusterID)
			}
			for i := range out {
				out[i].Clusters = membership[out[i].ID]
			}
		}
	}
	return out, window, nil
}
