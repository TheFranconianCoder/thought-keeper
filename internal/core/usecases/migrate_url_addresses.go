package usecases

import (
	"context"
	"errors"
	"io"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// errAddressTaken is returned when the address a URL source should move to is already in use. It is
// not a failure of the move: it is the vault saying that two rows claim one URL, which a reader has to
// resolve (the move leaves both alone and names them in the log).
var errAddressTaken = errors.New("the address is already taken")

// URLAddressMigrator moves URL sources onto their URL's address. A handler holds this value; nil
// means the feature is not wired.
type URLAddressMigrator func(ctx context.Context, apply bool) (MigrateURLAddressesResult, error)

// MigrateURLAddressesResult reports what the move found and what it did.
type MigrateURLAddressesResult struct {
	// Checked is how many URL sources were looked at (one per URL).
	Checked int
	// Moved is how many were still on the old address and were moved.
	Moved int
	// Atoms is how many claims had their provenance re-pointed.
	Atoms int
	// Skipped counts the sources that could not be moved (their address is already taken, or a file
	// could not be read): they stay where they are and are named in the log.
	Skipped int
	// Pending is every URL still on the old address — after the move, and as the whole answer when it
	// is only a preview.
	Pending []string
}

// Migrate moves every URL source onto its URL's address. apply=false is the preview.
func (uc URLs) Migrate(ctx context.Context, apply bool) (MigrateURLAddressesResult, error) {
	rows, err := uc.Index.ListURLSources(ctx)
	if err != nil {
		return MigrateURLAddressesResult{}, err
	}
	var res MigrateURLAddressesResult
	seen := make(map[string]bool)
	for _, s := range rows {
		if s.URL == "" || seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		res.Checked++

		want := entities.URLHash(s.URL)
		if s.Hash == want {
			continue
		}
		res.Pending = append(res.Pending, s.URL)
		if !apply {
			continue
		}
		atoms, err := uc.move(ctx, s, want)
		if err != nil {
			res.Skipped++
			if uc.Log != nil {
				uc.Log.Warn("url address: could not move the source", "url", s.URL, "from", s.Hash, "err", err)
			}
			continue
		}
		res.Moved++
		res.Atoms += atoms
		// The URL is at its address now: it is no longer pending.
		res.Pending = res.Pending[:len(res.Pending)-1]
	}
	if uc.Log != nil && (res.Moved > 0 || len(res.Pending) > 0) {
		uc.Log.Info("url addresses migrated", "checked", res.Checked, "moved", res.Moved,
			"atoms", res.Atoms, "skipped", res.Skipped, "pending", len(res.Pending), "apply", apply)
	}
	return res, nil
}

// move rewrites one URL source's address: the object, the row, the sidecars and the claims that cite
// it. It returns how many claims were re-pointed.
func (uc URLs) move(ctx context.Context, s entities.Source, want string) (int, error) {
	// The address must be free. A source already sitting there is the same URL (the list is per URL),
	// so this is a collision the caller must not walk into: leaving the source alone is the honest
	// answer, and the log names it.
	if _, err := uc.Index.GetSourceByHash(ctx, want); err == nil {
		return 0, errAddressTaken
	}

	rc, err := uc.Store.Open(s.Hash, s.Ext)
	if err != nil {
		return 0, err
	}
	body, readErr := io.ReadAll(rc)
	_ = rc.Close()
	if readErr != nil {
		return 0, readErr
	}
	obj, err := uc.Store.PutAt(want, s.Ext, body)
	if err != nil {
		return 0, err
	}
	if err := uc.Index.SetSourceHash(ctx, s.Hash, want, obj.Path); err != nil {
		return 0, err
	}
	// Everything derived from the source belongs to the source, not to its address.
	if err := uc.Store.RenameSidecars(s.Hash, want); err != nil {
		return 0, err
	}
	if uc.Steps != nil {
		if err := uc.Steps.Rehash(s.Hash, want); err != nil && uc.Log != nil {
			uc.Log.Warn("url address: the record kept the old address", "url", s.URL, "err", err)
		}
	}
	atoms, err := uc.repoint(ctx, s.Hash, want)
	if err != nil {
		return atoms, err
	}
	// The old object is a copy of the same bytes under a name nobody uses any more: to the trash.
	if err := uc.Store.Remove(ctx, s.Hash, s.Ext); err != nil && uc.Log != nil {
		uc.Log.Warn("url address: the old object stayed", "url", s.URL, "hash", s.Hash, "err", err)
	}
	return atoms, nil
}

// repoint rewrites the provenance of every claim of this source: the address it cites is the one it
// came from, and that address has moved. The claim itself, its body, its branch and its node are
// untouched — this is a rename, and a rename must not change what a claim says.
func (uc URLs) repoint(ctx context.Context, oldHash, newHash string) (int, error) {
	if uc.Atoms == nil || uc.AtomStore == nil {
		return 0, nil
	}
	atoms, err := uc.Atoms.ListAtomsBySource(ctx, oldHash)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range atoms {
		changed := false
		for i := range a.Sources {
			if a.Sources[i].Hash == oldHash {
				a.Sources[i].Hash = newHash
				changed = true
			}
		}
		if !changed {
			continue
		}
		// The FILE is written first (it is the truth); the index row is keyed by the claim's id, so it
		// needs nothing — the provenance edge it holds is by source ID, which did not move.
		if _, err := uc.AtomStore.Write(ctx, a); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("url address: claim not re-pointed", "atom", a.ID.String(), "err", err)
			}
			continue
		}
		n++
	}
	return n, nil
}
