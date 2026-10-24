package sourcemeta

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// StepIndex is the index side of the mirror (the sqlc source repository).
type StepIndex interface {
	SetSourceSteps(ctx context.Context, hash string, steps entities.StepSet) error
	SetSourceTitle(ctx context.Context, hash, title string) error
	SetSourceLanguage(ctx context.Context, hash, language string) error
	SetSourceOrigin(ctx context.Context, hash, origin, wikiPage, parentHash string) error
	ClearSourceOrigin(ctx context.Context, hash string) error
	SetSourceTags(ctx context.Context, hash string, tags []string) error
	SetSourceAutoTags(ctx context.Context, hash string, tags []string, at time.Time) error
}

// Tracker implements interfaces.StepTracker: it writes a source's document-step state into the
// vault (<hash>.meta — the truth) and mirrors it into the index, so the list view can answer
// "which steps is this document through?" without opening a file per row.
type Tracker struct {
	Meta  *Store
	Index StepIndex
	Log   *slog.Logger
}

// NewTracker returns a tracker over a sources/ directory and its index mirror.
func NewTracker(meta *Store, index StepIndex, log *slog.Logger) *Tracker {
	return &Tracker{Meta: meta, Index: index, Log: log}
}

var _ interfaces.StepTracker = (*Tracker)(nil)

// Steps returns a source's recorded document-step state.
func (t *Tracker) Steps(hash string) (entities.StepSet, error) {
	if t == nil || t.Meta == nil {
		return entities.NewStepSet(), nil
	}
	rec, err := t.Meta.Read(hash)
	if err != nil {
		return nil, err
	}
	return rec.Steps, nil
}

// Scan returns every recorded source's step state, keyed by hash.
func (t *Tracker) Scan() (map[string]entities.StepSet, error) {
	if t == nil || t.Meta == nil {
		return map[string]entities.StepSet{}, nil
	}
	recs, err := t.Meta.Scan()
	if err != nil {
		return nil, err
	}
	out := make(map[string]entities.StepSet, len(recs))
	for hash, rec := range recs {
		out[hash] = rec.Steps
	}
	return out, nil
}

// Records returns every source's record, keyed by hash. It is what a reindex rebuilds the index
// from: the titles, the step lights and the part records all live in the vault.
func (t *Tracker) Records() (map[string]entities.SourceRecord, error) {
	if t == nil || t.Meta == nil {
		return map[string]entities.SourceRecord{}, nil
	}
	recs, err := t.Meta.Scan()
	if err != nil {
		return nil, err
	}
	out := make(map[string]entities.SourceRecord, len(recs))
	for hash, rec := range recs {
		out[hash] = entities.SourceRecord{
			Identity: entities.SourceIdentity{
				Kind:         entities.SourceKind(rec.Kind),
				URL:          rec.URL,
				Bookmark:     rec.Bookmark,
				Language:     rec.Language,
				Origin:       rec.Origin,
				WikiPage:     rec.WikiPage,
				ParentHash:   rec.ParentHash,
				Sitemap:      rec.Sitemap,
				RSS:          rec.RSS,
				OriginalName: rec.OriginalName,
				ContentHash:  rec.ContentHash,
				FaviconHash:  rec.FaviconHash,
				FaviconExt:   rec.FaviconExt,
				Attached:     rec.Attached,
				AttachedFrom: rec.AttachedFrom,
				AttachedHash: rec.AttachedHash,
				FromFeed:     rec.FromFeed,
				FeedHash:     rec.FeedHash,
			},
			Steps: rec.Steps, Title: strings.TrimSpace(rec.Title), Tags: rec.Tags,
			AutoTags: rec.AutoTags, AutoTagsAt: unixOrZero(rec.AutoTagsAt), Parts: rec.Parts,
		}
	}
	return out, nil
}

// SetSourceTags writes the human's labels into the source record (the truth) and mirrors them into
// the index. The record is written first: a failed mirror must not lose the label.
func (t *Tracker) SetSourceTags(ctx context.Context, hash string, tags []string) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	if _, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.Tags = tags
		return nil
	}); err != nil {
		return err
	}
	if t.Index != nil {
		if err := t.Index.SetSourceTags(ctx, hash, tags); err != nil {
			if t.Log != nil {
				t.Log.Warn("source tags not mirrored into the index", "hash", hash, "err", err)
			}
			return err
		}
	}
	return nil
}

// SetSourceAutoTags writes the agent's labels into the record and the index. The timestamp is
// recorded even when the answer was empty: "tagged, nothing fit" must not be retried forever.
func (t *Tracker) SetSourceAutoTags(ctx context.Context, hash string, tags []string, at time.Time) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	if _, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.AutoTags = tags
		if at.IsZero() {
			rec.AutoTagsAt = 0
		} else {
			rec.AutoTagsAt = at.UnixMilli()
		}
		return nil
	}); err != nil {
		return err
	}
	if t.Index != nil {
		if err := t.Index.SetSourceAutoTags(ctx, hash, tags, at); err != nil {
			if t.Log != nil {
				t.Log.Warn("source auto tags not mirrored into the index", "hash", hash, "err", err)
			}
			return err
		}
	}
	return nil
}

// SourceTags returns every source's human and derived labels, keyed by hash. The vocabulary's rename
// reads it from the records: the .meta file is the truth, and a curation is rare enough for the walk.
func (t *Tracker) SourceTags(ctx context.Context) (map[string]entities.SourceTagSet, error) {
	recs, err := t.Records()
	if err != nil {
		return nil, err
	}
	out := make(map[string]entities.SourceTagSet, len(recs))
	for hash, rec := range recs {
		if len(rec.Tags) == 0 && len(rec.AutoTags) == 0 {
			continue
		}
		out[hash] = entities.SourceTagSet{
			Tags:     append([]string(nil), rec.Tags...),
			AutoTags: append([]string(nil), rec.AutoTags...), AutoTagsAt: rec.AutoTagsAt,
		}
	}
	return out, nil
}

func unixOrZero(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// SetParts records a run's part outcomes next to the source. The part record is the one piece of
// derived state the index cannot rebuild, so it is a file like everything else.
func (t *Tracker) SetParts(hash string, parts map[string]entities.PartRecord) error {
	if t == nil || t.Meta == nil || len(parts) == 0 {
		return nil
	}
	_, err := t.Meta.SetParts(hash, parts)
	return err
}

// Reset clears a source's derivation steps. Capture survives on purpose: a reset keeps the source
// (same id, same bytes, same metadata) and throws away only what was derived from it.
func (t *Tracker) Reset(hash string) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	var set entities.StepSet
	_, err := t.Meta.Update(hash, func(rec *Record) error {
		set = entities.NewStepSet()
		if st, ok := rec.Steps[entities.StepCapture]; ok {
			set[entities.StepCapture] = st
		}
		rec.Steps = set
		// The part record belongs to the derivation that was just thrown away.
		rec.Parts = nil
		return nil
	})
	if err != nil {
		return err
	}
	if t.Index != nil {
		if err := t.Index.SetSourceSteps(context.Background(), hash, set); err != nil && t.Log != nil {
			t.Log.Warn("step state not mirrored into the index", "hash", hash, "err", err)
		}
	}
	return nil
}

// Remove drops a source's step record (the source itself is gone).
func (t *Tracker) Remove(hash string) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	return t.Meta.Remove(hash)
}

// Rehash moves a source's record to another address: the file is renamed by the store, and the hash it
// carries inside is updated, so the next step write lands at the address the source now has.
func (t *Tracker) Rehash(oldHash, newHash string) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	_, err := t.Meta.Update(newHash, func(rec *Record) error {
		rec.Hash = newHash
		return nil
	})
	return err
}

// SetTitle stores a source's title in its record and mirrors it into the index. An empty title is
// ignored: a good title is never wiped by a step that could not determine one.
func (t *Tracker) SetTitle(hash, title string) error {
	title = strings.TrimSpace(title)
	if t == nil || t.Meta == nil || title == "" {
		return nil
	}
	if _, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.Title = title
		return nil
	}); err != nil {
		return err
	}
	// Mirror even when the record already carries the title. A RESET rewrites the index's title to
	// the intake name without touching the record; a later SetTitle would then see "unchanged" and
	// leave the index showing the wrong name — which the capture step would faithfully re-read.
	if t.Index != nil {
		if err := t.Index.SetSourceTitle(context.Background(), hash, title); err != nil && t.Log != nil {
			t.Log.Warn("title not mirrored into the index", "hash", hash, "err", err)
		}
	}
	return nil
}

// SetIdentity records WHAT a source is — its original kind, the address it was fetched from, whether
// it is a collected link, and the name it came in under.
//
// Nothing is mirrored into the index here: the identity is written once, by the same use case that
// upserts the row, so the index already carries it. Its reader is the REINDEX — an index built from
// the vault alone (a copied vault, a deleted database) has no other way to know that <hash>.md is a
// URL snapshot and not a Markdown source.
func (t *Tracker) SetIdentity(hash string, id entities.SourceIdentity) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	_, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.Kind = string(id.Kind)
		rec.URL = id.URL
		rec.Bookmark = id.Bookmark
		if id.Language != "" {
			rec.Language = id.Language
		}
		if id.Origin != "" {
			rec.Origin = id.Origin
		}
		if id.WikiPage != "" {
			rec.WikiPage = id.WikiPage
		}
		if id.ParentHash != "" {
			rec.ParentHash = id.ParentHash
		}
		rec.Sitemap = id.Sitemap
		rec.RSS = id.RSS
		if id.OriginalName != "" {
			rec.OriginalName = id.OriginalName
		}
		rec.ContentHash = id.ContentHash
		rec.FaviconHash = id.FaviconHash
		rec.FaviconExt = id.FaviconExt
		rec.Attached = id.Attached
		rec.AttachedFrom = id.AttachedFrom
		rec.AttachedHash = id.AttachedHash
		rec.FromFeed = id.FromFeed
		rec.FeedHash = id.FeedHash
		return nil
	})
	return err
}

// Identity returns what the source's record says the source IS. The consistency job asks it to find a
// record that LOST its identity (an interrupted write, a truncated file): the index still knows what
// the source was, and without the record a reindex would file a URL snapshot as Markdown.
func (t *Tracker) Identity(hash string) (entities.SourceIdentity, error) {
	if t == nil || t.Meta == nil {
		return entities.SourceIdentity{}, nil
	}
	rec, err := t.Meta.Read(hash)
	if err != nil {
		return entities.SourceIdentity{}, err
	}
	return entities.SourceIdentity{
		Kind:         entities.SourceKind(rec.Kind),
		URL:          rec.URL,
		Bookmark:     rec.Bookmark,
		Language:     rec.Language,
		Origin:       rec.Origin,
		WikiPage:     rec.WikiPage,
		ParentHash:   rec.ParentHash,
		Sitemap:      rec.Sitemap,
		RSS:          rec.RSS,
		OriginalName: rec.OriginalName,
		ContentHash:  rec.ContentHash,
		FaviconHash:  rec.FaviconHash,
		FaviconExt:   rec.FaviconExt,
	}, nil
}

// SetLanguage records the source's detected language in the vault record and mirrors it into the
// index. An empty language is ignored: a detector that could not classify a short sample must not
// erase a language another capture already found.
func (t *Tracker) SetLanguage(hash, language string) error {
	language = strings.TrimSpace(strings.ToLower(language))
	if t == nil || t.Meta == nil || language == "" {
		return nil
	}
	if _, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.Language = language
		return nil
	}); err != nil {
		return err
	}
	if t.Index != nil {
		if err := t.Index.SetSourceLanguage(context.Background(), hash, language); err != nil && t.Log != nil {
			t.Log.Warn("language not mirrored into the index", "hash", hash, "err", err)
		}
	}
	return nil
}

// SetOrigin marks a source as created by a wiki page. It is written to the record and mirrored into
// the index; removal uses it to distinguish a page-owned URL from an independently collected one.
func (t *Tracker) SetOrigin(hash, origin, wikiPage string) error {
	if t == nil || t.Meta == nil || origin == "" {
		return nil
	}
	if _, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.Origin = origin
		if wikiPage != "" {
			rec.WikiPage = wikiPage
		}
		return nil
	}); err != nil {
		return err
	}
	if t.Index != nil {
		if err := t.Index.SetSourceOrigin(context.Background(), hash, origin, wikiPage, ""); err != nil && t.Log != nil {
			t.Log.Warn("origin not mirrored into the index", "hash", hash, "err", err)
		}
	}
	return nil
}

// Release makes a source INDEPENDENT: the page, sitemap or feed that brought it in no longer owns it.
// It is what a human's own addition means when the URL was already in the vault as a sub-source — the
// manual source wins, so deleting the page or the sitemap must not delete it. The source itself stays
// exactly as it is; only the ownership goes.
func (t *Tracker) Release(hash string) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	if _, err := t.Meta.Update(hash, func(rec *Record) error {
		rec.Origin = ""
		rec.WikiPage = ""
		rec.ParentHash = ""
		return nil
	}); err != nil {
		return err
	}
	if t.Index != nil {
		if err := t.Index.ClearSourceOrigin(context.Background(), hash); err != nil && t.Log != nil {
			t.Log.Warn("release not mirrored into the index", "hash", hash, "err", err)
		}
	}
	return nil
}

// Record persists one step transition. The vault is written FIRST: the file is the truth, and a
// mirror that failed to follow is repaired by the next write or by a reindex.
func (t *Tracker) Record(ctx context.Context, hash string, step entities.Step, st entities.StepStatus) error {
	if t == nil || t.Meta == nil {
		return nil
	}
	rec, err := t.Meta.Set(hash, step, st)
	if err != nil {
		if t.Log != nil {
			t.Log.Warn("step state not recorded", "hash", hash, "step", step, "err", err)
		}
		return err
	}
	if t.Index != nil {
		if err := t.Index.SetSourceSteps(ctx, hash, rec.Steps); err != nil && t.Log != nil {
			t.Log.Warn("step state not mirrored into the index", "hash", hash, "step", step, "err", err)
		}
	}
	if t.Log != nil {
		level := slog.LevelDebug
		if st.State == entities.StepFailed {
			level = slog.LevelWarn
		}
		t.Log.Log(ctx, level, "source step",
			"hash", hash, "step", step, "state", st.State, "detail", st.Detail, "err", st.Error)
	}
	return nil
}
