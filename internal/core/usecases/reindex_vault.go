package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	coretree "github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Maintenance is the use case of the vault upkeep: the index rebuild, the reconcile that checks the
// records against the bytes, and the cleanup that prunes what the derived layer kept too long.
type Maintenance struct {
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Atoms     interfaces.AtomRepository
	AtomStore interfaces.AtomStore
	// Trees is the OUTLINE caches next to the sources (<hash>.outline). The index's source_nodes are
	// a mirror of them, so a rebuilt index restores the trees too — "delete index.db and reindex"
	// must not leave green lights over an empty tree. `rebuild-trees` re-derives the outlines from the
	// rendering; this just reads the cache that is already there.
	Trees interfaces.TreeStore
	Clock interfaces.Clock
	Log   *slog.Logger
	// MaintenanceRepo prunes child rows whose parent vanished.
	MaintenanceRepo interfaces.Maintenance
	// Clean removes a source derivation when it has to be rebuilt.
	Clean SourceCleaner
	Lint  interfaces.LintRepository
	// SourceLog receives a line for every vault file that could not be indexed, with the reason.
	SourceLog interfaces.SourceLog
	// Steps rebuilds the index's mirror of the document-step state from the vault's .meta files.
	Steps interfaces.StepTracker
	// Shelf restores the reader's marks (the star and the read-later queue) from the records: a
	// deleted index must not lose what the reader kept.
	Shelf interfaces.ShelfMirror
	// Parts restores the part records behind the distill step.
	Parts interfaces.PartRepository
	// Progress reports the rebuild to the reader: which phase runs and how far it is. Nil means no
	// report (a test, or a caller that does not serve a UI).
	Progress func(phase string, done, total int)
	// The association layer: its records are the truth, its rows the cache.
	ClusterStore interfaces.ClusterStore
	ClusterRepo  interfaces.ClusterRepository
	// Languages backfills the language of sources written before the field existed.
	Languages interfaces.LanguageDetector
	// Limit and Offset let a huge vault be checked in slices; DryRun only reports what it would repair.
	Limit  int
	Offset int
	DryRun bool
}

// ReindexResult reports what a reindex did. Skipped counts vault files that could not be indexed.
type ReindexResult struct {
	Scanned int
	Indexed int
	Added   int
	Removed int
	Atoms   int
	Skipped int
	// Steps counts the sources whose document-step state was mirrored from their .meta file.
	Steps int
	// PartsPruned counts stale part records removed in this pass (a node that is gone or has no text).
	PartsPruned int
	// Orphans are child rows whose parent had vanished.
	Orphans int64
	// AtomsRemoved counts index rows whose claim FILE is gone.
	AtomsRemoved int
	// AtomsOrphaned counts claims trashed because every source they cited is gone from the vault.
	AtomsOrphaned int
	// ProvenancePruned counts claims whose provenance lost a source that is gone.
	ProvenancePruned int
	// Clusters counts the association records mirrored into the index.
	Clusters int
	// ClustersRemoved counts cluster rows (or records) that had no claim left.
	ClustersRemoved int
	// objects is the set of source hashes the vault's bytes hold. Plumbing for the claim phase: a
	// source is only gone when neither its record nor its object is there. Unexported — it is not a
	// number the reader sees.
	objects map[string]bool
}

// ReindexService rebuilds the index from the vault. A handler holds this value; nil means the feature
// is not wired.
type ReindexService func(ctx context.Context) (ReindexResult, error)

// Reindex rebuilds the index from the vault.
func (uc Maintenance) Reindex(ctx context.Context) (ReindexResult, error) {
	// Referential repair first: an index written while foreign keys were off carries child
	// rows without parents.
	var orphans int64
	if uc.MaintenanceRepo != nil {
		n, err := uc.MaintenanceRepo.PruneOrphans(ctx)
		if err != nil && uc.Log != nil {
			uc.Log.Warn("reindex: prune orphans", "err", err)
		}
		if err == nil {
			orphans = n
		}
		if orphans > 0 && uc.Log != nil {
			uc.Log.Warn("reindex: removed orphaned rows", "rows", orphans)
		}
	}
	// The source records (the <hash>.meta files) are the vault's truth about what a source IS and what
	// it has been through. They are read ONCE and used by both phases below. Without them the rebuild
	// must not run: every claim would look like it had lost its source.
	records, err := uc.records()
	if err != nil {
		return ReindexResult{}, err
	}
	res, err := uc.sources(ctx, records)
	res.Orphans = orphans
	if err != nil {
		return res, err
	}
	res = uc.steps(ctx, res, records)
	res = uc.prunePartRecords(ctx, res)
	res, err = uc.derived(ctx, res, records)
	if err != nil {
		return res, err
	}
	res, err = uc.clusters(ctx, res)
	if err != nil {
		return res, err
	}
	if res.Skipped > 0 {
		// A partial atom phase must not re-arm part records: a claim that simply was not indexed
		// would look like a part that was never derived, and the pipeline would re-distill the whole
		// document — that is the loop this guard exists to break.
		if uc.Log != nil {
			uc.Log.Warn("reindex: the atom phase was incomplete — part records are not re-armed",
				"skipped", res.Skipped)
		}
		return res, nil
	}
	return uc.reconcileMeta(ctx, res, records), nil
}

// reconcileMeta makes the source's .meta agree with the atoms that are actually on disk.
//
// A part recorded `done` whose node no atom cites is not done: it is marked incomplete with reason
// `missing_atoms` (and the distill step is put back to pending) BEFORE the scheduler reads the record.
// A `source_relative` part is the exception: it is metadata and correctly has no atom.
func (uc Maintenance) reconcileMeta(ctx context.Context, res ReindexResult, records map[string]entities.SourceRecord) ReindexResult {
	if uc.Steps == nil || uc.Atoms == nil || len(records) == 0 {
		return res
	}
	for hash, rec := range records {
		if len(rec.Parts) == 0 || rec.Steps.State(entities.StepDistill) == entities.StepSkipped {
			continue
		}
		refs, err := uc.Atoms.NodeRefsBySource(ctx, hash)
		if err != nil {
			continue
		}
		claimed := make(map[string]bool, len(refs))
		for _, nodes := range refs {
			for _, node := range nodes {
				claimed[node] = true
			}
		}
		changed := make(map[string]entities.PartRecord)
		for node, part := range rec.Parts {
			if part.State != entities.PartDone || part.Reason == "source_relative" || claimed[node] {
				continue
			}
			part.State = entities.PartIncomplete
			part.Reason = "missing_atoms"
			part.Atoms = 0
			part.At = uc.Clock.Now()
			changed[node] = part
		}
		if len(changed) == 0 {
			continue
		}
		if uc.Parts != nil {
			if src, sErr := uc.Index.GetSourceByHash(ctx, hash); sErr == nil {
				for node, part := range changed {
					if pErr := uc.Parts.SetPartState(ctx, src.ID, entities.PartState{
						NodeID: node, State: part.State, Reason: part.Reason,
						Atoms: part.Atoms, UpdatedAt: part.At,
					}); pErr != nil && uc.Log != nil {
						uc.Log.Warn("reindex: part record not reset", "hash", hash, "node", node, "err", pErr)
					}
				}
			}
		}
		if err := uc.Steps.SetParts(hash, changed); err != nil && uc.Log != nil {
			uc.Log.Warn("reindex: part records not written back", "hash", hash, "err", err)
		}
		if err := uc.Steps.Record(ctx, hash, entities.StepDistill, entities.StepStatus{
			State: entities.StepPending, At: uc.Clock.Now(),
			Detail: fmt.Sprintf("reindex: %d part(s) have no atoms — derive again", len(changed)),
		}); err != nil && uc.Log != nil {
			uc.Log.Warn("reindex: distill step not reopened", "hash", hash, "err", err)
		}
		if uc.Log != nil {
			uc.Log.Warn("reindex: stale part records reset — their atoms are gone",
				"hash", hash, "parts", len(changed))
		}
		if uc.SourceLog != nil {
			uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
				At: uc.Clock.Now(), Phase: "reindex", Level: "warn",
				Message: "stale part records reset — their atoms are gone",
				Fields:  map[string]any{"parts": len(changed)},
			})
		}
	}
	return res
}

// prunePartRecords drops part records that no longer describe a text-bearing node. They are the
// leftovers of a tree rebuild (the synthetic root, a removed section) and would otherwise keep a
// done light over a part that does not exist. A source with no node rows at all is left alone: its
// outline may simply not be restored yet.
func (uc Maintenance) prunePartRecords(ctx context.Context, res ReindexResult) ReindexResult {
	if uc.Parts == nil {
		return res
	}
	n, err := uc.Parts.PruneWithoutText(ctx)
	if err != nil {
		if uc.Log != nil {
			uc.Log.Warn("reindex: stale part records not pruned", "err", err)
		}
		return res
	}
	res.PartsPruned = int(n)
	if n > 0 && uc.Log != nil {
		uc.Log.Info("reindex: stale part records pruned", "parts", n)
	}
	return res
}

// records reads every source's <hash>.meta record, keyed by hash.
func (uc Maintenance) records() (map[string]entities.SourceRecord, error) {
	if uc.Steps == nil {
		return nil, fmt.Errorf("reindex: the source records are not wired — refusing to rebuild the index")
	}
	records, err := uc.Steps.Records()
	if err != nil {
		return nil, fmt.Errorf("reindex: read the source records: %w", err)
	}
	return records, nil
}

// steps mirrors the document-step state of every source from its .meta file into the index, and the
// TITLE with it: the title is determined in step 1 and lives in that file.
func (uc Maintenance) steps(ctx context.Context, res ReindexResult, records map[string]entities.SourceRecord) ReindexResult {
	if uc.Steps == nil || uc.Index == nil || records == nil {
		return res
	}
	for hash, rec := range records {
		if err := uc.Index.SetSourceSteps(ctx, hash, rec.Steps); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("reindex: step state not mirrored", "hash", hash, "err", err)
			}
			continue
		}
		if rec.Title != "" {
			if err := uc.Index.SetSourceTitle(ctx, hash, rec.Title); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: title not restored", "hash", hash, "err", err)
			}
		}
		// The reader's marks live in the record too. Restore them even when empty: a stale star or
		// queue entry in a reused index must not survive the rebuild.
		if uc.Shelf != nil {
			if err := uc.Shelf.SetSourceFavorite(ctx, hash, rec.FavoriteAt); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: favorite not restored", "hash", hash, "err", err)
			}
			if err := uc.Shelf.SetSourceReadLater(ctx, hash, rec.ReadLaterAt, rec.ReadLaterDoneAt, rec.ReadLaterManual, rec.ReadLaterVia); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: read later not restored", "hash", hash, "err", err)
			}
		}
		if len(rec.Parts) > 0 && uc.Parts != nil {
			src, sErr := uc.Index.GetSourceByHash(ctx, hash)
			if sErr != nil {
				continue
			}
			for node, p := range rec.Parts {
				state := entities.PartState{
					NodeID: node, State: p.State, Reason: p.Reason, Atoms: p.Atoms, UpdatedAt: p.At,
				}
				if err := uc.Parts.SetPartState(ctx, src.ID, state); err != nil && uc.Log != nil {
					uc.Log.Warn("reindex: part record not restored", "hash", hash, "node", node, "err", err)
				}
			}
		}
		res.Steps++
	}
	return res
}

// progress reports one phase of the rebuild, when a reader is listening.
func (uc Maintenance) progress(phase string, done, total int) {
	if uc.Progress != nil {
		uc.Progress(phase, done, total)
	}
}

// sourceExists reports whether the vault still holds a source: its record, its bytes, or an index row
// that outlived both. A lookup that FAILS is an error, never "gone" — treating a canceled context as
// a missing source is what turned one timed-out rebuild into a mass delete.
func sourceExists(
	ctx context.Context, index interfaces.IndexRepository,
	records map[string]entities.SourceRecord, objects map[string]bool, hash string,
) (bool, error) {
	if _, ok := records[hash]; ok {
		return true, nil
	}
	if objects[hash] {
		return true, nil
	}
	if index == nil {
		return false, nil
	}
	_, err := index.GetSourceByHash(ctx, hash)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, types.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("reindex: source lookup failed for %s: %w", hash, err)
	}
}

// derived rebuilds the claim index from the files. It is the destructive phase of a rebuild, so
// every decision it makes comes from the VAULT: the files that are there and the records that say
// which source they belong to. The index — the thing being rebuilt — never decides that a claim is
// gone. A missing index row is "not yet indexed", not "lost".
func (uc Maintenance) derived(ctx context.Context, res ReindexResult, records map[string]entities.SourceRecord) (ReindexResult, error) {
	if uc.AtomStore == nil || uc.Atoms == nil {
		return res, nil
	}
	if records == nil {
		return res, fmt.Errorf("reindex: no source records — refusing to rebuild the claims")
	}
	atoms, unreadable, err := uc.AtomStore.Scan(ctx)
	if err != nil {
		return res, err
	}
	if len(unreadable) > 0 {
		// The vault holds claim files this pass cannot read. Dropping their rows — or worse, deleting
		// the files — would turn a read problem into data loss: refuse the whole rebuild.
		return res, fmt.Errorf("reindex: %d claim file(s) cannot be read (first: %s) — nothing was rebuilt",
			len(unreadable), unreadable[0])
	}
	// A row whose file is gone is stale: the vault is the truth and `reindex` rebuilds the index FROM
	// it. It is dropped and counted, so "0 removed" is never a silent lie.
	known := make(map[string]bool, len(atoms))
	for _, a := range atoms {
		known[a.ID.String()] = true
	}
	existing, err := uc.Atoms.AtomAddresses(ctx)
	if err != nil {
		return res, fmt.Errorf("reindex: read the claim rows: %w", err)
	}
	for _, a := range existing {
		if known[a.ID.String()] {
			continue
		}
		// The row goes; the FILE is never touched here. If the scan could not read it, this pass
		// aborted above; if it is really gone, there is nothing left to remove. Deleting a file
		// because it was not indexed is how a repair destroys the vault.
		if err := uc.Atoms.DeleteAtom(ctx, a.ID); err != nil {
			res.Skipped++
			continue
		}
		res.AtomsRemoved++
	}
	for i, a := range atoms {
		if i%500 == 0 {
			uc.progress("claims", i, len(atoms))
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		// THE PIPELINE IS COMPLETE, OR NOTHING AT THE BACK HANGS ON IT. A claim whose provenance names
		// a source the VAULT no longer holds cannot be attributed — it goes (file to .trash/atoms/,
		// row dropped). A PARTIAL provenance is pruned instead: the claim keeps the sources that
		// exist, and the FILE is rewritten, or the next reindex reads the dead hash again.
		if len(a.Sources) > 0 {
			kept := a.Sources[:0]
			for _, src := range a.Sources {
				ok, sErr := sourceExists(ctx, uc.Index, records, res.objects, src.Hash)
				if sErr != nil {
					// A lookup that FAILED is not a source that is gone. Abort: the claims stay.
					return res, sErr
				}
				if ok {
					kept = append(kept, src)
				}
			}
			if len(kept) == 0 {
				if err := uc.AtomStore.Trash(ctx, a); err != nil {
					if uc.Log != nil {
						uc.Log.Warn("reindex: orphaned claim not trashed", "atom", a.ID.String(), "err", err)
					}
					res.Skipped++
					continue
				}
				res.AtomsOrphaned++
				if uc.Log != nil {
					uc.Log.Warn("reindex: claim trashed — the vault holds no record of any source it cites",
						"atom", a.ID.String(), "sources", len(a.Sources), "claim", a.Claim)
				}
				continue
			}
			if len(kept) < len(a.Sources) {
				a.Sources = kept
				if _, err := uc.AtomStore.Write(ctx, a); err != nil && uc.Log != nil {
					uc.Log.Warn("reindex: pruned provenance not written", "atom", a.ID.String(), "err", err)
				}
				res.ProvenancePruned++
			}
		}
		if err := uc.Atoms.SaveAtom(ctx, a); err != nil {
			res.Skipped++
			if uc.Log != nil {
				uc.Log.Warn("reindex: atom not indexed", "atom", a.ID.String(), "err", err)
			}
			uc.logSources(ctx, a.Sources, "warn", "atom not indexed", map[string]any{
				"atom": a.ID.String(), "claim": a.Claim, "err": err,
			})
			continue
		}
		res.Atoms++
	}
	uc.progress("claims", len(atoms), len(atoms))
	return res, nil
}

// clusters rebuilds the association layer FROM ITS RECORDS. The record is the truth of a cluster (its
// id and members), the row is the queryable mirror; a record that lost every member is removed, and a
// row whose record is gone is deleted. There is no dirty/successor machinery: the layer is a cache.
func (uc Maintenance) clusters(ctx context.Context, res ReindexResult) (ReindexResult, error) {
	if uc.ClusterStore == nil || uc.ClusterRepo == nil {
		return res, nil
	}
	records, err := uc.ClusterStore.ScanMetas(ctx)
	if err != nil {
		return res, err
	}
	// The existence check needs ids only; a record that lost no member needs nothing else either.
	// The signature of a PRUNED record is the one case that reads the claim text, and it is the rare
	// case — so the text read is deferred to it instead of costing every claim of the vault.
	existing, err := uc.Atoms.AtomIDs(ctx)
	if err != nil {
		return res, err
	}
	exists := make(map[ulid.ULID]bool, len(existing))
	for _, id := range existing {
		exists[id] = true
	}
	type prunedRecord struct {
		rec  entities.Cluster
		kept []ulid.ULID
	}
	var lost []prunedRecord
	want := make(map[ulid.ULID]bool, len(records))
	for i, rec := range records {
		if i%100 == 0 {
			uc.progress("clusters", i, len(records))
		}
		kept := make([]ulid.ULID, 0, len(rec.Atoms))
		for _, id := range rec.Atoms {
			if exists[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) == 0 {
			if err := uc.ClusterStore.RemoveMeta(ctx, rec.ID); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: empty cluster record not removed", "cluster", rec.ID.String(), "err", err)
			}
			_ = uc.ClusterRepo.DeleteCluster(ctx, rec.ID) //nolint:errcheck // a trash move is best effort; a missing file is the goal
			res.ClustersRemoved++
			continue
		}
		if len(kept) != len(rec.Atoms) {
			rec.Atoms = kept
			lost = append(lost, prunedRecord{rec: rec, kept: kept})
			continue
		}
		if err := uc.ClusterRepo.SaveCluster(ctx, rec); err != nil {
			res.Skipped++
			if uc.Log != nil {
				uc.Log.Warn("reindex: cluster not indexed", "cluster", rec.ID.String(), "err", err)
			}
			continue
		}
		want[rec.ID] = true
		res.Clusters++
	}
	if len(lost) > 0 {
		// The signature of a pruned record is computed from the members it KEEPS: read exactly
		// those, never the corpus.
		ids := make([]ulid.ULID, 0, len(lost)*8)
		for i := range lost {
			ids = append(ids, lost[i].kept...)
		}
		atoms, err := uc.Atoms.AtomTextsForIDs(ctx, uniqueULIDs(ids))
		if err != nil {
			return res, err
		}
		byID := atomsByID(atoms)
		for i := range lost {
			rec := lost[i].rec
			rec.Signature = subjectSignature(atomsFor(lost[i].kept, byID))
			rec.Updated = uc.Clock.Now()
			if err := uc.ClusterStore.WriteMeta(ctx, rec); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: pruned cluster record not written", "cluster", rec.ID.String(), "err", err)
			}
			if err := uc.ClusterRepo.SaveCluster(ctx, rec); err != nil {
				res.Skipped++
				if uc.Log != nil {
					uc.Log.Warn("reindex: cluster not indexed", "cluster", rec.ID.String(), "err", err)
				}
				continue
			}
			want[rec.ID] = true
			res.Clusters++
		}
	}
	rows, err := uc.ClusterRepo.ListClusters(ctx, 1_000_000, 0)
	if err != nil {
		return res, err
	}
	for _, row := range rows {
		if want[row.ID] {
			continue
		}
		if err := uc.ClusterRepo.DeleteCluster(ctx, row.ID); err != nil && uc.Log != nil {
			uc.Log.Warn("reindex: stale cluster row not removed", "cluster", row.ID.String(), "err", err)
		}
		res.ClustersRemoved++
	}
	uc.progress("clusters", len(records), len(records))
	return res, nil
}

func atomsByID(atoms []entities.Atom) map[string]entities.Atom {
	out := make(map[string]entities.Atom, len(atoms))
	for _, a := range atoms {
		out[a.ID.String()] = a
	}
	return out
}

// logSources writes one event to every source a file belongs to.
func (uc Maintenance) logSources(ctx context.Context, sources []entities.AtomSource, level, msg string, fields map[string]any) {
	if uc.SourceLog == nil {
		return
	}
	seen := map[string]bool{}
	for _, s := range sources {
		if s.Hash == "" || seen[s.Hash] {
			continue
		}
		seen[s.Hash] = true
		uc.SourceLog.Append(ctx, s.Hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: "reindex", Level: level, Message: msg, Fields: fields,
		})
	}
}

// cleanNavigationNodes removes the text of a breadcrumb/menu from an outline written before the
// navigation filter existed. The node stays as a grouping placeholder, but it has no text and is not
// a part any more.
func cleanNavigationNodes(nodes []entities.SourceNode) bool {
	changed := false
	for i := range nodes {
		n := &nodes[i]
		if strings.TrimSpace(n.Title) != "" || !coretree.IsNavigationText(n.Summary) {
			continue
		}
		if n.TextHash == "" && n.Text == "" && n.TextStart == 0 && n.TextEnd == 0 && n.Summary == "" {
			continue
		}
		n.Text = ""
		n.TextHash = ""
		n.TextStart = 0
		n.TextEnd = 0
		n.HasText = false
		n.Summary = ""
		changed = true
	}
	return changed
}

// sources reconciles the source index with the content-addressed store.
func (uc Maintenance) sources(ctx context.Context, records map[string]entities.SourceRecord) (ReindexResult, error) {
	objs, err := uc.Store.Scan(ctx)
	if err != nil {
		return ReindexResult{}, err
	}
	existing, err := uc.Index.SourceHashes(ctx)
	if err != nil {
		return ReindexResult{}, err
	}
	existingSet := make(map[string]bool, len(existing))
	for _, h := range existing {
		existingSet[h] = true
	}
	kinds, kErr := uc.Index.SourceKinds(ctx)
	if kErr != nil && uc.Log != nil {
		uc.Log.Warn("reindex: read the indexed kinds", "err", kErr)
	}

	res := ReindexResult{Scanned: len(objs)}
	seen := make(map[string]bool, len(objs))
	res.objects = seen
	for i, o := range objs {
		if i%100 == 0 {
			uc.progress("sources", i, len(objs))
		}
		// WHAT the source IS comes from its record first: a URL snapshot, a Markdown source and a
		// PDF's readable rendering are all <hash>.md — the record is the only thing that tells them
		// apart. What the index already knows beats the file name; the extension is the last resort.
		record, hasRecord := records[o.Hash]
		identity := entities.SourceIdentity{Kind: entities.KindForExt(o.Ext)}
		if hasRecord && record.Identity.Kind != "" {
			identity = record.Identity
		} else if k := kinds[o.Hash]; k != "" {
			identity.Kind = entities.SourceKind(k)
		}
		if identity.Kind == "" {
			continue
		}
		seen[o.Hash] = true
		src := entities.Source{
			ID:           ulid.Make(),
			Hash:         o.Hash,
			Kind:         identity.Kind,
			Ext:          o.Ext,
			Path:         o.Path,
			URL:          identity.URL,
			Bookmark:     identity.Bookmark,
			OriginalName: identity.OriginalName,
			Language:     identity.Language,
			Origin:       identity.Origin,
			WikiPage:     identity.WikiPage,
			ParentHash:   identity.ParentHash,
			Sitemap:      identity.Sitemap,
			RSS:          identity.RSS,
			Listing:      identity.Listing,
			FromFeed:     identity.FromFeed,
			FeedHash:     identity.FeedHash,
			Tags:         record.Tags,
			AutoTags:     record.AutoTags,
			AutoTagsAt:   record.AutoTagsAt,
			Bytes:        o.Bytes,
			TreeState:    entities.TreeNone,
			ImportedAt:   uc.Clock.Now(),
			Meta:         "{}",
		}
		if err := uc.Index.UpsertSource(ctx, src); err != nil {
			return res, err
		}
		if identity.Language != "" {
			if err := uc.Index.SetSourceLanguage(ctx, o.Hash, identity.Language); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: language not restored", "hash", o.Hash, "err", err)
			}
		}
		if identity.Origin != "" {
			if err := uc.Index.SetSourceOrigin(ctx, o.Hash, identity.Origin, identity.WikiPage, identity.ParentHash); err != nil && uc.Log != nil {
				uc.Log.Warn("reindex: origin not restored", "hash", o.Hash, "err", err)
			}
		}
		// The human tags are a record too. Set them even when empty: the record is the truth, and a
		// stale label in a reused index must not survive a rebuild.
		if err := uc.Index.SetSourceTags(ctx, o.Hash, record.Tags); err != nil && uc.Log != nil {
			uc.Log.Warn("reindex: source tags not restored", "hash", o.Hash, "err", err)
		}
		if err := uc.Index.SetSourceAutoTags(ctx, o.Hash, record.AutoTags, record.AutoTagsAt); err != nil && uc.Log != nil {
			uc.Log.Warn("reindex: source auto tags not restored", "hash", o.Hash, "err", err)
		}
		// The OUTLINE is a cache next to the source. Restore its rows here: `source_nodes` is what the
		// tree API and the progress bars read, and a rebuilt index must not show "outline ok" over an
		// empty tree.
		if uc.Trees != nil {
			if nodes, nErr := uc.Trees.Read(ctx, o.Hash); nErr == nil && len(nodes) > 0 {
				if cleanNavigationNodes(nodes) {
					_ = uc.Trees.Write(ctx, o.Hash, nodes) //nolint:errcheck // the index and the step records are a light; the vault is the truth
				}
				if stored, sErr := uc.Index.GetSourceByHash(ctx, o.Hash); sErr == nil {
					if err := uc.Index.ReplaceSourceNodes(ctx, stored.ID, nodes); err != nil {
						if uc.Log != nil {
							uc.Log.Warn("reindex: tree not restored", "hash", o.Hash, "err", err)
						}
					} else {
						pages := 0
						for _, n := range nodes {
							if n.EndPage > pages {
								pages = n.EndPage
							}
						}
						title := stored.Title
						if rec, ok := records[o.Hash]; ok && rec.Title != "" {
							title = rec.Title
						}
						if err := uc.Index.SetSourceTree(ctx, o.Hash, string(entities.TreeBuilt), title, pages); err != nil && uc.Log != nil {
							uc.Log.Warn("reindex: tree state not restored", "hash", o.Hash, "err", err)
						}
					}
				}
			}
		}
		// A URL snapshot's metadata (its content hash, its icon, whether a human attached it) is the
		// vault's, not the index's.
		if rec, ok := records[o.Hash]; ok && (rec.Identity.ContentHash != "" || rec.Identity.Attached) {
			var fav *faviconMeta
			if rec.Identity.FaviconHash != "" {
				fav = &faviconMeta{Hash: rec.Identity.FaviconHash, Ext: rec.Identity.FaviconExt}
			}
			// The record knows the ownership too, and this write replaces the row's whole meta JSON:
			// carrying it here is what keeps a reindex from un-owning a link a wiki page brought in.
			meta := metaWithIdentity(metaJSON(urlMeta{
				ContentHash: rec.Identity.ContentHash, URL: rec.Identity.URL, Favicon: fav,
				Attached: rec.Identity.Attached, AttachedFrom: rec.Identity.AttachedFrom,
				AttachedHash: rec.Identity.AttachedHash,
			}), entities.Source{
				Language: rec.Identity.Language, Origin: rec.Identity.Origin,
				WikiPage: rec.Identity.WikiPage, ParentHash: rec.Identity.ParentHash,
				Sitemap: rec.Identity.Sitemap, RSS: rec.Identity.RSS, Listing: rec.Identity.Listing,
				FromFeed: rec.Identity.FromFeed, FeedHash: rec.Identity.FeedHash,
			})
			if err := uc.Index.SetSourceMeta(ctx, o.Hash, meta); err != nil {
				return res, err
			}
			if rec.Identity.Language != "" {
				if err := uc.Index.SetSourceLanguage(ctx, o.Hash, rec.Identity.Language); err != nil && uc.Log != nil {
					uc.Log.Warn("reindex: language not restored after URL meta", "hash", o.Hash, "err", err)
				}
			}
		}
		res.Indexed++
		if !existingSet[o.Hash] {
			res.Added++
		}
	}
	// A URL source that was never fetched has a RECORD but no object: a wiki page linked it and the
	// fetch did not answer (yet, or at all). It is not an orphan — the record is the truth about a link
	// the vault knows — so it is restored from the record and kept. Deleting it would make a dead link
	// vanish on the next reindex, which is exactly what the reader must be able to see.
	for hash, rec := range records {
		if seen[hash] || rec.Identity.Kind != entities.SourceKindURL {
			continue
		}
		switch rec.Steps.State(entities.StepCapture) {
		case entities.StepOK, entities.StepSkipped:
			continue // a URL record that claims a snapshot with no object is damage, not a link
		}
		src := entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
			URL: rec.Identity.URL, Title: rec.Title, OriginalName: rec.Identity.OriginalName,
			Bookmark: rec.Identity.Bookmark, Language: rec.Identity.Language,
			Origin: rec.Identity.Origin, WikiPage: rec.Identity.WikiPage,
			ParentHash: rec.Identity.ParentHash, Sitemap: rec.Identity.Sitemap,
			RSS:       rec.Identity.RSS,
			Listing:   rec.Identity.Listing,
			FromFeed:  rec.Identity.FromFeed,
			FeedHash:  rec.Identity.FeedHash,
			TreeState: entities.TreeNone, ImportedAt: uc.Clock.Now(), Meta: "{}",
		}
		if src.Title == "" {
			src.Title = src.URL
		}
		if src.OriginalName == "" {
			src.OriginalName = src.URL
		}
		if err := uc.Index.UpsertSource(ctx, src); err != nil {
			return res, err
		}
		seen[hash] = true
		res.Indexed++
		if !existingSet[hash] {
			res.Added++
		}
	}
	for h := range existingSet {
		if !seen[h] {
			if err := uc.Index.DeleteSourceByHash(ctx, h); err != nil {
				return res, err
			}
			res.Removed++
		}
	}
	return res, nil
}
