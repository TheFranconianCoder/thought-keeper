package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// FixURLsResult reports what the repair looked at and what it fixed.
type FixURLsResult struct {
	Checked int
	Fixed   int
}

// URLKindFixer repairs sources whose index entry lost their kind. A handler holds this value; nil
// means the feature is not wired.
type URLKindFixer func(ctx context.Context) (FixURLsResult, error)

// FixKinds repairs the sources that lost the fact that they are URL snapshots.
func (uc Sources) FixKinds(ctx context.Context) (FixURLsResult, error) {
	var res FixURLsResult
	sources, err := uc.Index.ListSources(ctx, entities.SourceFilter{}, 1_000_000, 0)
	if err != nil {
		return res, err
	}
	for _, src := range sources {
		if src.Kind == entities.SourceKindURL || src.Kind == entities.SourceKindPDF || src.Ext != ".md" {
			// Already a URL, or not a Markdown object: nothing to guess. A PDF's object is not .md, and
			// a Markdown source's frontmatter may declare anything — only a URL is worth repairing.
			continue
		}
		res.Checked++
		url, err := uc.Declared.DeclaredURL(ctx, src.Hash, src.Ext)
		if err != nil || url == "" {
			continue
		}
		if err := uc.Index.SetSourceKindURL(ctx, src.Hash, url); err != nil {
			if uc.Log != nil {
				uc.Log.Warn("fix urls: could not repair the row", "hash", src.Hash, "err", err)
			}
			continue
		}
		// The record is the truth and the index mirrors it: write it there too, or the next reindex
		// undoes the repair. Preserve every field the record already had (the favicon and content
		// hash above all): this repair changes WHAT the source is, not what was derived from it.
		if uc.Steps != nil {
			id := entities.SourceIdentity{Kind: entities.SourceKindURL, URL: url}
			if recs, rErr := uc.Steps.Records(); rErr == nil {
				if rec, ok := recs[src.Hash]; ok {
					id = rec.Identity
					id.Kind, id.URL = entities.SourceKindURL, url
				}
			}
			if err := uc.Steps.SetIdentity(src.Hash, id); err != nil && uc.Log != nil {
				uc.Log.Warn("fix urls: could not write the record", "hash", src.Hash, "err", err)
			}
		}
		res.Fixed++
		if uc.Log != nil {
			uc.Log.Info("fix urls: a source that had lost its kind is a URL again", "hash", src.Hash, "url", url)
		}
	}
	return res, nil
}
