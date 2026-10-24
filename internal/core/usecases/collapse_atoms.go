package usecases

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// CollapseDuplicateAtoms finds claims that are the SAME claim written more than once and keeps one of
// them. Same means: same topic, statement, provenance, body, type and status — the copies differ in
// nothing but their ID.
//
// Which copy is kept: one a CLUSTER cites wins (then no association loses its claim), otherwise the
// oldest. Everything else moves to the vault's `.trash/atoms/` — trash, not delete. The association
// layer is a cache; the next pass prunes the records that lost a member.
type CollapseDuplicateAtoms struct {
	Atoms    interfaces.AtomStore
	AtomRepo interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
	Clock    interfaces.Clock
	Log      *slog.Logger
}

// Detect reports the duplicate claims without touching anything.
func (uc CollapseDuplicateAtoms) Detect(ctx context.Context) (partialentities.PartialAtomDuplicates, error) {
	groups, err := uc.groups(ctx)
	if err != nil {
		return partialentities.PartialAtomDuplicates{}, err
	}
	out := partialentities.PartialAtomDuplicates{}
	for _, g := range groups {
		out.Groups++
		out.Files += len(g)
		if out.Example == "" {
			out.Example = g[0].Claim
		}
	}
	return out, nil
}

// Execute collapses them: the copies go to the trash and their index rows go with them.
func (uc CollapseDuplicateAtoms) Execute(ctx context.Context) (partialentities.PartialAtomDuplicates, error) {
	groups, err := uc.groups(ctx)
	if err != nil {
		return partialentities.PartialAtomDuplicates{}, err
	}
	member, err := uc.clustered(ctx, groups)
	if err != nil {
		return partialentities.PartialAtomDuplicates{}, err
	}
	out := partialentities.PartialAtomDuplicates{}
	for _, g := range groups {
		keep := pickKept(g, member)
		out.Groups++
		out.Files += len(g)
		if out.Example == "" {
			out.Example = keep.Claim
		}
		for _, a := range g {
			if a.ID == keep.ID {
				continue
			}
			if err := uc.AtomRepo.DeleteAtom(ctx, a.ID); err != nil {
				uc.warn("delete atom row", a.ID, err)
				continue
			}
			if err := uc.Atoms.Trash(ctx, a); err != nil {
				uc.warn("trash atom file", a.ID, err)
				continue
			}
			out.Collapsed++
		}
	}
	if uc.Log != nil {
		uc.Log.Info("duplicate claims collapsed", "groups", out.Groups, "files", out.Files, "collapsed", out.Collapsed)
	}
	return out, nil
}

// groups is the claims that exist more than once, keyed by everything that makes a claim a claim.
//
// It reads the INDEX, in id order, and hashes the identity: the file-store walk it used to do read
// and decoded every atom file (frontmatter, sources, links) to answer a 200-byte question, and the
// PWA asks that question twice at start-up — a ~770 MB spike for "no duplicates". The index holds
// the same identity and the start-up consistency check is what keeps it in step with the vault.
//
// Only hashes that COLLIDE are visited again: the few claims behind them are loaded whole (that is
// what a collapse needs to trash a file) and re-keyed, so a hash collision can never make two
// different claims look like copies.
func (uc CollapseDuplicateAtoms) groups(ctx context.Context) ([][]entities.Atom, error) {
	type duplicate struct {
		count int
		first ulid.ULID
		ids   []ulid.ULID
	}
	byHash := make(map[[sha256.Size]byte]duplicate, 4096)
	err := uc.AtomRepo.StreamClaimIdentities(ctx, func(a entities.Atom) error {
		h := sha256.Sum256([]byte(claimKey(a)))
		d := byHash[h]
		if d.count == 0 {
			byHash[h] = duplicate{count: 1, first: a.ID}
			return nil
		}
		d.count++
		if len(d.ids) == 0 {
			d.ids = []ulid.ULID{d.first, a.ID}
		} else {
			d.ids = append(d.ids, a.ID)
		}
		byHash[h] = d
		return nil
	})
	if err != nil {
		return nil, err
	}

	byKey := map[string][]entities.Atom{}
	for _, d := range byHash {
		if d.count < 2 {
			continue
		}
		for _, id := range d.ids {
			a, err := uc.AtomRepo.GetAtom(ctx, id)
			if err != nil {
				uc.warn("load duplicate", id, err)
				continue
			}
			byKey[claimKey(a)] = append(byKey[claimKey(a)], a)
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out [][]entities.Atom
	for _, k := range keys {
		g := byKey[k]
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(i, j int) bool { return g[i].ID.Compare(g[j].ID) < 0 })
		out = append(out, g)
	}
	return out, nil
}

// claimKey is everything a claim IS: what it says, where it belongs, where it came from.
func claimKey(a entities.Atom) string {
	sources := make([]string, 0, len(a.Sources))
	for _, s := range a.Sources {
		ocr := "0"
		if s.OCR {
			ocr = "1"
		}
		sources = append(sources, s.Hash+"\x1f"+s.NodeID+"\x1f"+ocr)
	}
	sort.Strings(sources)
	return strings.Join([]string{
		a.Topic, a.Claim, string(a.Type), string(a.Status), strings.Join(sources, "\x1e"), a.Body,
	}, "\x00")
}

// pickKept chooses the copy that stays: one a cluster cites wins, otherwise the oldest.
func pickKept(group []entities.Atom, member map[ulid.ULID]bool) entities.Atom {
	for _, a := range group {
		if member[a.ID] {
			return a
		}
	}
	return group[0]
}

// clustered is the set of claims some cluster owns, asked only about the claims that are actually
// duplicated — the collapse needs no other membership.
func (uc CollapseDuplicateAtoms) clustered(ctx context.Context, groups [][]entities.Atom) (map[ulid.ULID]bool, error) {
	out := map[ulid.ULID]bool{}
	if uc.Clusters == nil || len(groups) == 0 {
		return out, nil
	}
	ids := make([]ulid.ULID, 0, len(groups)*2)
	for _, g := range groups {
		for _, a := range g {
			ids = append(ids, a.ID)
		}
	}
	links, err := uc.Clusters.MembershipsForAtoms(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		out[l.AtomID] = true
	}
	return out, nil
}

func (uc CollapseDuplicateAtoms) warn(msg string, id ulid.ULID, err error) {
	if uc.Log != nil {
		uc.Log.Warn("collapse duplicates: "+msg, "atom", id.String(), "err", err)
	}
}
