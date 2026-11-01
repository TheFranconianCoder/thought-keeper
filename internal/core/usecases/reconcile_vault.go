package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// ReconcileResult reports what one pass found and repaired.
type ReconcileResult struct {
	Checked int
	// Invalidated are the sources whose derivation was thrown away (an artefact a step claimed is
	// gone) and that must be derived again.
	Invalidated []string
	// Broken are the sources whose OBJECT is gone: there is nothing left to derive from.
	Broken []string
	// MissingSummaries are sources whose derivation is complete but whose summary sidecar is gone —
	// or was never written, which is every source from before the summary existed. This pass does not
	// write them: a summary is a model call, and the app queues it on its own worker.
	MissingSummaries []string
	// MissingSourceTags are completely distilled sources that were never auto-tagged. The pass only
	// reports them: deriving labels is a model call, queued on the tagging worker.
	MissingSourceTags []string
	// Next is the offset the next pass should start at (0 again once the vault was walked to its end).
	Next int
	// Languages counts sources whose missing language was detected and recorded in this pass.
	Languages int
	// Identities counts records that had lost what the source IS and were restored from the index.
	Identities int
	// Ownerships counts ROWS that had lost their owner (origin/wiki_page/parent) and were restored
	// from the record. It is reported apart from Identities because it is a different symptom: the
	// source page showed no owner, and removal safety had no answer.
	Ownerships int
	// Repairs counts the writes this pass made (or, in a DryRun, would make): invalidated sources,
	// broken objects, restored identities and backfilled languages.
	Repairs int
}

// reopenStaleDistill reopens a distill step whose record says through while the index holds fewer
// finished parts than text-bearing nodes. A derivation that STOPPED must not look finished: the pump
// derives the missing parts, and a run that finds the outline's references stale rebuilds it. This
// is what heals a source on the regular consistency pass instead of leaving it stale for ever.
func (uc Maintenance) reopenStaleDistill(ctx context.Context, src entities.Source, res *ReconcileResult) bool {
	if src.Bookmark || uc.Steps == nil || uc.Index == nil {
		return false
	}
	switch src.Steps.State(entities.StepDistill) {
	case entities.StepOK, entities.StepPending, entities.StepFailed:
		// A failed distill with parts missing is retried here; one whose parts are all recorded keeps
		// its failure (the run said why, and a retry would say the same).
	default:
		return false
	}
	full, err := uc.Index.GetSourceByHash(ctx, src.Hash)
	if err != nil || full.PartsTotal == 0 || full.PartsDone >= full.PartsTotal {
		return false
	}
	if err := uc.Steps.Record(ctx, src.Hash, entities.StepDistill, entities.StepStatus{
		State: entities.StepPending, At: uc.Clock.Now(),
		Detail: fmt.Sprintf("reconcile: %d of %d parts recorded — derive again", full.PartsDone, full.PartsTotal),
	}); err != nil {
		uc.log(ctx, src.Hash, "warn", "reconcile", "could not reopen the distill step", map[string]any{"err": err})
		return false
	}
	res.Repairs++
	uc.log(ctx, src.Hash, "warn", "reconcile", "the distill step was reopened: parts are missing", map[string]any{
		"parts_done": full.PartsDone, "parts_total": full.PartsTotal,
	})
	return true
}

// Reconcile checks every record against the vault and repairs what drifted.
func (uc Maintenance) Reconcile(ctx context.Context) (ReconcileResult, error) {
	if uc.Index == nil || uc.Store == nil {
		return ReconcileResult{}, nil
	}
	limit := uc.Limit
	if limit <= 0 {
		limit = 500
	}
	srcs, err := uc.Index.ListSources(ctx, entities.SourceFilter{}, limit, uc.Offset)
	if err != nil {
		return ReconcileResult{}, err
	}

	res := ReconcileResult{Next: uc.Offset + len(srcs)}
	if len(srcs) < limit {
		// The walk reached the end of the vault: the next pass starts over.
		res.Next = 0
	}
	for _, src := range srcs {
		// A record can lose its IDENTITY (an interrupted write, a truncated file): it still has its
		// steps but no longer says what the source is. The index knows — it learned it from that very
		// record — so writing it back restores what was dropped instead of inventing anything. Without
		// this a URL snapshot would come back as Markdown on the next reindex, with the address it came
		// from gone.
		uc.healIdentity(ctx, src, &res)
		// The mirror can also lose the OWNERSHIP (origin/wiki_page/parent) while the record still
		// knows it: a meta write that rebuilt the JSON from the snapshot fields dropped it, and the
		// source page then showed no wiki page while removal safety had no answer. The record is the
		// truth; copy it back.
		uc.healOwnership(ctx, src, &res)

		// Backfill the language before the bookmark/derivation checks: every source can carry the
		// hint, and an old vault enters the search with the right morphology on the next pass.
		if src.Language == "" && uc.Languages != nil {
			if language := uc.detectLanguage(ctx, src); language != "" {
				res.Languages++
				res.Repairs++
				if !uc.DryRun {
					if uc.Steps != nil {
						_ = uc.Steps.SetLanguage(src.Hash, language) //nolint:errcheck // the index and the step records are a light; the vault is the truth
					}
					uc.log(ctx, src.Hash, "info", "language", "source language detected", map[string]any{"language": language})
				}
			}
		}
		if src.Bookmark {
			// A bookmark has no derivation: nothing to check, nothing to lose.
			continue
		}
		res.Checked++

		if _, ok := uc.Store.StatObject(src.Hash, src.Ext); !ok {
			if src.Kind == entities.SourceKindURL && src.Path == "" {
				// A link a page declared and the fetch never answered: there IS no object, by design.
				// The record already says so (a red capture light with the reason), and reporting it as
				// a broken source would cry wolf on every pass until somebody deletes the page's link.
				continue
			}
			// The source itself is gone. Nothing can be derived from it, and no reindex can bring it
			// back — say it loudly instead of leaving a green light over a missing document.
			res.Broken = append(res.Broken, src.Hash)
			res.Repairs++
			uc.log(ctx, src.Hash, "error", "reconcile", "the source object is missing", nil)
			if uc.Steps != nil && !uc.DryRun {
				_ = uc.Steps.Record(ctx, src.Hash, entities.StepCapture, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
					State: entities.StepFailed, At: uc.Clock.Now(),
					Error: "the source object is missing from the store",
				})
			}
			continue
		}

		missing := ""
		switch {
		case src.Steps.State(entities.StepOutline) == entities.StepOK && !hasSidecar(uc.Store, src.Hash, entities.SidecarOutline):
			missing = "outline"
		case src.NeedsCapture() &&
			src.Steps.State(entities.StepCapture) == entities.StepOK && !hasSidecar(uc.Store, src.Hash, entities.SidecarRender):
			missing = "rendering"
		}
		if missing == "" {
			if uc.reopenStaleDistill(ctx, src, &res) {
				// The derivation is not through; its summary and labels wait for the parts.
				continue
			}
			if !src.Bookmark && src.Steps.State(entities.StepDistill) == entities.StepOK &&
				!hasSidecar(uc.Store, src.Hash, entities.SidecarSummary) {
				res.MissingSummaries = append(res.MissingSummaries, src.Hash)
			}
			if !src.Bookmark && src.Steps.State(entities.StepDistill) == entities.StepOK && src.AutoTagsAt.IsZero() {
				res.MissingSourceTags = append(res.MissingSourceTags, src.Hash)
			}
			continue
		}

		res.Invalidated = append(res.Invalidated, src.Hash)
		res.Repairs++
		if uc.DryRun {
			continue
		}
		// Everything derived from the missing artefact is wrong: throw the derivation away and put
		// the step record back to where the file belongs, so the pipeline builds it again.
		cleaned, cErr := uc.Clean(ctx, src.Hash)
		if cErr != nil {
			uc.log(ctx, src.Hash, "warn", "reconcile", "could not invalidate the derivation",
				map[string]any{"err": cErr})
		}
		if err := uc.Index.SetSourceTree(ctx, src.Hash, string(entities.TreeNone), "", 0); err != nil {
			uc.log(ctx, src.Hash, "warn", "reconcile", "could not reset the outline state",
				map[string]any{"err": err})
		}
		if uc.Steps != nil {
			// Reset keeps `capture` (the source is still readable); a missing RENDERING is exactly the
			// case where capture has to run again.
			_ = uc.Steps.Reset(src.Hash) //nolint:errcheck // the index and the step records are a light; the vault is the truth
			if missing == "rendering" {
				_ = uc.Steps.Record(ctx, src.Hash, entities.StepCapture, entities.StepStatus{ //nolint:errcheck // the index and the step records are a light; the vault is the truth
					State: entities.StepPending, At: uc.Clock.Now(), Detail: "the rendering is gone — capture again",
				})
			}
		}
		uc.log(ctx, src.Hash, "warn", "reconcile", "an artefact is missing — derivation invalidated",
			map[string]any{"missing": missing, "atoms_removed": cleaned.Atoms})
	}
	return res, nil
}

// healIdentity writes a lost identity back into the source's record. A record without a kind is not a
// Markdown file: it is a record that lost what it knew, and the index — which mirrors that record — is
// the only place the answer still exists.
func (uc Maintenance) healIdentity(ctx context.Context, src entities.Source, res *ReconcileResult) {
	if uc.Steps == nil || src.Kind == "" {
		return
	}
	id, err := uc.Steps.Identity(src.Hash)
	if err != nil || id.Kind != "" {
		return
	}
	healed := entities.SourceIdentity{
		Kind: src.Kind, URL: src.URL, Bookmark: src.Bookmark, Language: src.Language,
		Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash,
		Sitemap: src.Sitemap, RSS: src.RSS,
		FromFeed: src.FromFeed, FeedHash: src.FeedHash,
		OriginalName: src.OriginalName,
		ContentHash:  contentHashFromMeta(src.Meta), FaviconHash: faviconHash(faviconFromMeta(src.Meta)),
		FaviconExt: faviconExt(faviconFromMeta(src.Meta)),
	}
	// An attached snapshot must stay attached through the repair: without the flag the next reindex
	// would forget it, and the next refresh would replace the human's article with a login page.
	var meta urlMeta
	if err := json.Unmarshal([]byte(src.Meta), &meta); err == nil {
		healed.Attached, healed.AttachedFrom, healed.AttachedHash = meta.Attached, meta.AttachedFrom, meta.AttachedHash
	}
	if uc.DryRun {
		res.Identities++
		res.Repairs++
		return
	}
	if err := uc.Steps.SetIdentity(src.Hash, healed); err != nil {
		uc.log(ctx, src.Hash, "warn", "reconcile", "the source record lost its identity and could not be restored",
			map[string]any{"err": err})
		return
	}
	res.Identities++
	res.Repairs++
	uc.log(ctx, src.Hash, "warn", "reconcile", "the source record lost its identity — restored from the index",
		map[string]any{"kind": string(src.Kind), "url": src.URL})
}

// healOwnership restores the ownership a ROW lost while its record still knows it. Ownership is what
// the source page shows ("linked from a page") and what removal safety reads: a URL may only leave the
// vault with its owner's link. The record (<hash>.meta) is the truth; `origin`/`wiki_page`/
// `parent_hash` live only in the row's meta JSON, so a snapshot write that rebuilt that JSON from the
// snapshot's own fields could erase them — which is exactly what happened to links a wiki page queued
// before the merge existed. Nothing is invented: every value comes from the record.
func (uc Maintenance) healOwnership(ctx context.Context, src entities.Source, res *ReconcileResult) {
	if uc.Steps == nil || src.Kind == "" {
		return
	}
	id, err := uc.Steps.Identity(src.Hash)
	if err != nil || id.Kind == "" || id.Origin == "" {
		return
	}
	if src.Origin == id.Origin && src.WikiPage == id.WikiPage && src.ParentHash == id.ParentHash {
		return
	}
	res.Ownerships++
	res.Repairs++
	if uc.DryRun {
		return
	}
	if err := uc.Index.SetSourceOrigin(ctx, src.Hash, id.Origin, id.WikiPage, id.ParentHash); err != nil {
		uc.log(ctx, src.Hash, "warn", "reconcile", "the source row lost its owner and could not be restored",
			map[string]any{"err": err})
		return
	}
	uc.log(ctx, src.Hash, "warn", "reconcile", "the source row lost its owner — restored from the record",
		map[string]any{"origin": id.Origin, "wiki_page": id.WikiPage, "parent_hash": id.ParentHash})
}

// detectLanguage samples the readable text first and the outline second. The outline fallback is
// what repairs sources whose object is mostly code or markup.
func (uc Maintenance) detectLanguage(ctx context.Context, src entities.Source) string {
	if uc.Languages == nil {
		return ""
	}
	if text := languageSample(ctx, uc.Store, src); text != "" {
		if language := uc.Languages.Detect(text); language != "" {
			return language
		}
	}
	if uc.Trees == nil || src.TreeState != entities.TreeBuilt {
		return ""
	}
	nodes, err := uc.Trees.Read(ctx, src.Hash)
	if err != nil || len(nodes) == 0 {
		return ""
	}
	var sample strings.Builder
	for _, node := range nodes {
		if sample.Len() >= 32<<10 {
			break
		}
		sample.WriteString(node.Title)
		sample.WriteString(". ")
		sample.WriteString(node.Summary)
		sample.WriteByte('\n')
	}
	return uc.Languages.Detect(sample.String())
}

// languageSample reads only enough of a source's readable text to identify a language. It uses the
// capture rendering when there is one (a PDF, a wiki page representation) and the object otherwise.
func languageSample(_ context.Context, store interfaces.SourceStore, src entities.Source) string {
	var (
		rc  io.ReadCloser
		err error
	)
	if src.NeedsCapture() {
		rc, err = store.OpenRendered(src.Hash, entities.SidecarRender)
	} else {
		rc, err = store.Open(src.Hash, src.Ext)
	}
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 128<<10)
	n, _ := io.ReadFull(rc, buf) //nolint:errcheck // a short read is the expected outcome; n says how much arrived
	if n <= 0 {
		return ""
	}
	return string(buf[:n])
}

// hasSidecar is the one-line question the checks ask ("is the file that this step claims there?").
func hasSidecar(store interfaces.SourceStore, hash, suffix string) bool {
	_, ok := store.StatSidecar(hash, suffix)
	return ok
}

func (uc Maintenance) log(ctx context.Context, hash, level, phase, msg string, fields map[string]any) {
	if uc.SourceLog != nil {
		uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
			At: uc.Clock.Now(), Phase: phase, Level: level, Message: msg, Fields: fields,
		})
	}
	if uc.Log != nil {
		switch level {
		case "error":
			uc.Log.Error(msg, "hash", hash, "fields", fields)
		case "info":
			uc.Log.Info(msg, "hash", hash, "fields", fields)
		default:
			uc.Log.Warn(msg, "hash", hash, "fields", fields)
		}
	}
}
