package usecases

import (
	"context"
	"io"
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// DeleteResult reports what was removed.
type DeleteResult struct {
	AtomsRemoved      int
	DependentsRemoved int
	// Rehomed counts the sources that were NOT deleted because another owner still declares them.
	// Their ownership moved to that owner; their bytes, claims and log stayed.
	Rehomed int
	// Removed are the hashes this call actually deleted (a batch's own hashes, not its subtree).
	// A hash that is missing was re-homed.
	Removed []string
}

// deletion is the state of ONE delete call, shared by the whole cascade: it is what stops a cycle in
// a malformed ownership graph from deleting the same source twice.
type deletion struct {
	seen map[string]bool
	// declarations is built once per delete action: every URL an owner still declares. It is what
	// keeps a source alive when the owner that brought it in goes away — a wiki page that linked it,
	// a listing that still lists it. Building it costs one pass over the list snapshots and the
	// wiki pages, so it is built lazily and never per child.
	declarations map[string][]declaration
	declared     bool
}

// declaration is one owner's claim on a URL. `priority` breaks a tie: a complete list is the
// strongest claim, a titleless window next, a human page last.
type declaration struct {
	origin     string
	wikiPage   string
	parentHash string
	priority   int
}

// SourceDeleter removes one source and everything derived from it. A handler holds this value; nil
// means the feature is not wired.
type SourceDeleter func(ctx context.Context, hash string) (DeleteResult, error)

// Delete removes one source and everything derived from it.
func (uc Sources) Delete(ctx context.Context, hash string) (DeleteResult, error) {
	return uc.delete(cancelProof(ctx), hash, false, &deletion{seen: map[string]bool{}})
}

// SourceOwnedDeleter removes a source that belongs to an owner: the cascade path another use case
// holds.
type SourceOwnedDeleter func(ctx context.Context, hashes ...string) (DeleteResult, error)

// DeleteOwned removes sources that belong to an owner, in ONE scope. A batch shares the declaration
// map, so a URL that another owner still declares survives every one of them without re-reading the
// lists per child.
func (uc Sources) DeleteOwned(ctx context.Context, hashes ...string) (DeleteResult, error) {
	d := &deletion{seen: map[string]bool{}}
	var out DeleteResult
	var firstErr error
	for _, hash := range hashes {
		if d.seen[hash] {
			continue
		}
		res, err := uc.delete(cancelProof(ctx), hash, true, d)
		out.AtomsRemoved += res.AtomsRemoved
		out.DependentsRemoved += res.DependentsRemoved
		out.Rehomed += res.Rehomed
		if res.Rehomed == 0 && err == nil {
			out.Removed = append(out.Removed, hash)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return out, firstErr
}

// cancelProof detaches a DELETE from its request. The human confirmed it; a browser that navigates
// away or gives up must not leave a half-deleted owner behind (that is exactly what happened: a feed
// with 30 children lost its children and kept the feed, because the request context was canceled
// mid-cascade and every later read failed with "context canceled").
func cancelProof(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (uc Sources) delete(ctx context.Context, hash string, allowDependent bool, d *deletion) (DeleteResult, error) {
	if d.seen[hash] {
		return DeleteResult{}, nil
	}
	d.seen[hash] = true
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return DeleteResult{}, err
	}
	if !allowDependent {
		switch {
		case src.Origin == entities.SitemapOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a sitemap; delete the sitemap instead", types.ErrConflict)
		case src.Origin == entities.RSSOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a feed; delete the feed instead", types.ErrConflict)
		case src.Origin == entities.ListingOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a listing; delete the listing instead", types.ErrConflict)
		case src.Origin == entities.WikiLinkOrigin:
			return DeleteResult{}, types.New("dependent_source",
				"this source belongs to a wiki page; delete the page instead", types.ErrConflict)
		case src.IsWiki():
			return DeleteResult{}, types.New("dependent_source",
				"this source is a wiki page relationship; delete the page instead", types.ErrConflict)
		}
	}

	// A source that another owner still declares is KEPT and re-homed, not deleted. Removing a wiki
	// page must not take a source a listing still lists, and removing a listing must not take one a
	// page still links: the URL is the source's identity, and its last declaration is what keeps it.
	if src.Origin != "" && uc.rehome(ctx, d, src) {
		return DeleteResult{Rehomed: 1}, nil
	}

	// A sitemap, a feed or a listing owns the pages it declared. Removing it removes the whole
	// subtree (a feed's items too: that is the only way its children ever leave); a child list recurses through
	// the same path, and the shared `seen` stops a cycle in a malformed ownership graph. A page that
	// disappeared from a FEED was kept while the feed existed — deleting the feed is what deletes it.
	var dependents DeleteResult
	if src.OwnsChildren() {
		rows, listErr := uc.Index.ListURLSources(ctx)
		if listErr != nil {
			if uc.Log != nil {
				uc.Log.Warn("delete owner: children not listed", "hash", hash, "err", listErr)
			}
		} else {
			for _, child := range rows {
				if child.ParentHash != hash || child.Origin == "" {
					continue
				}
				removed, childErr := uc.delete(ctx, child.Hash, true, d)
				if childErr != nil {
					if uc.Log != nil {
						uc.Log.Warn("delete owner: child not removed", "owner", hash, "child", child.Hash, "err", childErr)
					}
					continue
				}
				dependents.Rehomed += removed.Rehomed
				if removed.Rehomed == 0 {
					dependents.DependentsRemoved += 1 + removed.DependentsRemoved
				}
				dependents.AtomsRemoved += removed.AtomsRemoved
			}
		}
	}
	citing, err := uc.Atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		// Without the index answer we cannot know what cites this source: deleting it would leave
		// claims pointing at nothing. Stop instead of guessing.
		return DeleteResult{}, err
	}

	// The deletion is the source's last step: write it down before the work below unwinds
	// (the row goes, the log file stays in the index, next to the source's history).
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "delete", Level: "warn", Message: "source deleted",
			Fields: map[string]any{
				"kind": src.Kind, "title": src.Title, "original": src.OriginalName,
				"atoms": len(citing), "imported_at": src.ImportedAt.Format("2006-01-02T15:04:05Z"),
			},
		})
	}

	if err := uc.Index.DeleteSourceByHash(ctx, hash); err != nil {
		return DeleteResult{}, err
	}

	res := DeleteResult{
		AtomsRemoved:      dependents.AtomsRemoved,
		DependentsRemoved: dependents.DependentsRemoved,
		Rehomed:           dependents.Rehomed,
	}
	for _, a := range citing {
		n, err := uc.Atoms.CountAtomSources(ctx, a.ID)
		if err != nil || n > 0 {
			continue
		}
		if err := uc.Atoms.DeleteAtom(ctx, a.ID); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("delete source: atom row", "atom", a.ID.String(), "err", err)
			}
			continue
		}
		if err := uc.AtomStore.Remove(ctx, a); err != nil && uc.Log != nil {
			uc.Log.Warn("delete source: atom file", "atom", a.ID.String(), "err", err)
		}
		res.AtomsRemoved++
	}

	// The whole story of the source goes with it: the object (to the trash — bytes are recoverable,
	// but the source no longer exists) and EVERY sidecar next to it — the readable rendering, the
	// step record, the outline and the log. A source is one folder entry; deleting it deletes what
	// belongs to it, so nothing of a source that no longer exists can linger and be mistaken for
	// knowledge.
	if uc.SourceLog != nil {
		// Last line before the log is removed: what a deletion leaves behind is in the PROCESS log
		// (the per-source file is gone with the source).
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "delete", Level: "warn", Message: "deletion finished",
			Fields: map[string]any{
				"atoms_removed": res.AtomsRemoved, "object": "moved to trash",
			},
		})
	}
	if err := uc.Store.Remove(ctx, hash, src.Ext); err != nil && uc.Log != nil {
		uc.Log.Warn("delete source: object", "hash", hash, "err", err)
	}
	if err := uc.Store.RemoveSidecars(hash, src.Ext); err != nil && uc.Log != nil {
		uc.Log.Warn("delete source: sidecars", "hash", hash, "err", err)
	}
	if uc.Log != nil {
		uc.Log.Info("source deleted", "hash", hash, "kind", string(src.Kind),
			"atoms", res.AtomsRemoved, "sidecars", "removed", "object", "trashed")
	}
	if uc.Steps != nil {
		if err := uc.Steps.Remove(hash); err != nil && uc.Log != nil {
			uc.Log.Warn("delete: drop the step record", "hash", hash, "err", err)
		}
	}
	return res, nil
}

// rehome moves a source to the owner that still declares it and reports whether it did. Ownership
// is the last owner standing: the source keeps its bytes, its claims and its log, and only the
// record's owner changes (which is what the source page shows).
func (uc Sources) rehome(ctx context.Context, d *deletion, src entities.Source) bool {
	key := entities.URLKey(src.URL)
	if key == "" {
		return false
	}
	uc.declare(ctx, d)
	for _, decl := range d.declarations[key] {
		if sameOwner(src, decl) {
			// The owner being deleted names it too — that is not a reason to keep it.
			continue
		}
		if decl.parentHash != "" && d.seen[decl.parentHash] {
			continue
		}
		// Clear first: a source that leaves a wiki page must not keep `wiki_page` behind, and the
		// claim write only adds non-empty keys. Then the record (the truth a reindex reads) follows.
		if err := uc.Index.ClearSourceOrigin(ctx, src.Hash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("delete: source ownership not cleared", "hash", src.Hash, "err", err)
			}
			return false
		}
		if err := uc.Index.SetSourceOrigin(ctx, src.Hash, decl.origin, decl.wikiPage, decl.parentHash); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("delete: source not re-homed", "hash", src.Hash, "owner", decl.parentHash, "err", err)
			}
			return false
		}
		if uc.Steps != nil {
			if err := uc.Steps.SetOwnership(src.Hash, decl.origin, decl.wikiPage, decl.parentHash); err != nil && uc.Log != nil {
				uc.Log.Warn("delete: ownership not recorded", "hash", src.Hash, "err", err)
			}
		}
		appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "info", "kept — still declared by its "+ownerWord(decl.origin),
			map[string]any{"owner": decl.parentHash, "owner_origin": decl.origin, "wiki_page": decl.wikiPage})
		if uc.Log != nil {
			uc.Log.Info("source kept — re-homed to the owner that still declares it",
				"hash", src.Hash, "url", src.URL, "owner", decl.parentHash, "origin", decl.origin)
		}
		return true
	}
	return false
}

// sameOwner reports whether a declaration is the ownership the source already has — the owner that
// is being deleted and therefore cannot keep it.
func sameOwner(src entities.Source, decl declaration) bool {
	if decl.origin == entities.WikiLinkOrigin {
		return src.Origin == entities.WikiLinkOrigin && src.WikiPage == decl.wikiPage
	}
	return src.Origin == decl.origin && src.ParentHash == decl.parentHash
}

// declare builds the declaration map once per delete action. Priorities: sitemap (a complete list)
// beats feed (a window) beats listing (a window read from a layout) beats a wiki page (prose).
func (uc Sources) declare(ctx context.Context, d *deletion) {
	if d.declared {
		return
	}
	d.declared = true
	d.declarations = map[string][]declaration{}
	add := func(raw string, decl declaration) {
		key := entities.URLKey(raw)
		if key == "" {
			return
		}
		d.declarations[key] = append(d.declarations[key], decl)
	}

	if rows, err := uc.Index.ListURLSources(ctx); err == nil {
		for _, src := range rows {
			origin, priority := "", -1
			switch {
			case src.Sitemap:
				origin, priority = entities.SitemapOrigin, 0
			case src.RSS:
				origin, priority = entities.RSSOrigin, 1
			case src.Listing:
				origin, priority = entities.ListingOrigin, 2
			}
			if priority < 0 {
				continue
			}
			for _, item := range declaredURLs(uc.Store, src) {
				add(item, declaration{origin: origin, parentHash: src.Hash, priority: priority})
			}
		}
	}
	if uc.Pages != nil {
		if pages, err := uc.Pages.List(ctx); err == nil {
			for _, page := range pages {
				for _, raw := range markdown.URLs(page.Body) {
					add(raw, declaration{origin: entities.WikiLinkOrigin, wikiPage: page.ID.String(), priority: 3})
				}
			}
		}
	}
	for key := range d.declarations {
		sort.Slice(d.declarations[key], func(i, j int) bool {
			a, b := d.declarations[key][i], d.declarations[key][j]
			if a.priority != b.priority {
				return a.priority < b.priority
			}
			return a.parentHash+a.wikiPage < b.parentHash+b.wikiPage
		})
	}
}

// declaredURLs reads the addresses a list source's snapshot declares. The parse is of our own
// rendering (`- [title](url)`), never of a page — which is why a sitemap, a feed and a listing all
// answer it with the same reader.
func declaredURLs(store interfaces.SourceStore, src entities.Source) []string {
	if store == nil || src.Hash == "" || src.Ext == "" {
		return nil
	}
	rc, err := store.Open(src.Hash, src.Ext)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	items := parseListingSnapshot(string(body))
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.URL)
	}
	return out
}

func ownerWord(origin string) string {
	switch origin {
	case entities.SitemapOrigin:
		return "sitemap"
	case entities.RSSOrigin:
		return "feed"
	case entities.ListingOrigin:
		return "listing"
	default:
		return "wiki page"
	}
}
