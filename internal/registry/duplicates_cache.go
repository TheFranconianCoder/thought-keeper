package registry

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// duplicatesTTL is how long one detection serves every caller. The duplicate check is a maintenance
// question, and the PWA asks it twice at start-up (the Status mount and the catch-up). The answer is
// cheap to recompute, and a collapse invalidates it.
const duplicatesTTL = 30 * time.Second

// cachedDuplicates serves the duplicate detection from one shared scan and drops it after a
// collapse. The reader gets the last answer at once; a stale one is re-read behind the page.
type cachedDuplicates struct {
	uc    usecases.Claims
	cache *reportCache[partialentities.PartialAtomDuplicates]
}

func newCachedDuplicates(uc usecases.Claims) *cachedDuplicates {
	return &cachedDuplicates{uc: uc, cache: &reportCache[partialentities.PartialAtomDuplicates]{
		compute: uc.Detect,
		ttl:     duplicatesTTL,
	}}
}

func (c *cachedDuplicates) Detect(ctx context.Context) (partialentities.PartialAtomDuplicates, error) {
	return c.cache.Get(ctx)
}

// Execute collapses and drops the cached answer: the next reader must see the vault as it is now.
func (c *cachedDuplicates) Execute(ctx context.Context) (partialentities.PartialAtomDuplicates, error) {
	res, err := c.uc.Collapse(ctx)
	c.cache.Invalidate()
	return res, err
}

// Warm runs the first scan (see reportCache.Warm).
func (c *cachedDuplicates) Warm() { c.cache.Warm() }
