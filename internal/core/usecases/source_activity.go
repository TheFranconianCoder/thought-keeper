package usecases

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// activityRootsLimit caps the main sources the statistic names. The number beside them counts the
// batch, so a day with a big crawl still reads as a count and a few chips.
const activityRootsLimit = 24

// SourceActivity is the front door's statistic: how many sources arrived or changed since Start's
// watermark, and which MAIN sources they belong to.
type SourceActivity struct {
	Arrived SourceActivityGroup
	Changed SourceActivityGroup
}

// SourceActivityGroup is one half of the statistic. Count is the batch — a feed's five items count
// five — Roots is how many main sources it touches, and Main names up to activityRootsLimit of
// them: the feed stands for its articles, the sitemap for its pages.
type SourceActivityGroup struct {
	Count int
	Roots int
	Main  []partialentities.PartialSource
}

// SourceActivityReader answers the statistic for one instant. A handler holds this value; nil means
// the feature is not wired.
type SourceActivityReader func(ctx context.Context, since time.Time) (SourceActivity, error)

// Activity answers the front door's statistic against Start's own watermark: the sources that
// arrived after it, and the existing pages whose content changed. A zero instant has no baseline
// and answers nothing — the scope's first visit marks no source.
func (uc Sources) Activity(ctx context.Context, since time.Time) (SourceActivity, error) {
	if since.IsZero() {
		return SourceActivity{}, nil
	}
	arrived, err := uc.activity(ctx, entities.SourceFilter{ArrivedSince: since})
	if err != nil {
		return SourceActivity{}, err
	}
	changed, err := uc.activity(ctx, entities.SourceFilter{ChangedSince: since})
	if err != nil {
		return SourceActivity{}, err
	}
	return SourceActivity{Arrived: arrived, Changed: changed}, nil
}

func (uc Sources) activity(ctx context.Context, filter entities.SourceFilter) (SourceActivityGroup, error) {
	count, err := uc.Index.CountSources(ctx, filter)
	if err != nil {
		return SourceActivityGroup{}, err
	}
	if count == 0 {
		return SourceActivityGroup{}, nil
	}
	roots, err := uc.Index.CountSourceActivityRoots(ctx, filter)
	if err != nil {
		return SourceActivityGroup{}, err
	}
	main, err := uc.Index.ListSourceActivityRoots(ctx, filter, activityRootsLimit)
	if err != nil {
		return SourceActivityGroup{}, err
	}
	out := SourceActivityGroup{Count: count, Roots: roots, Main: make([]partialentities.PartialSource, 0, len(main))}
	for _, src := range main {
		out.Main = append(out.Main, partialentities.PartialFromSource(src))
	}
	return out, nil
}
