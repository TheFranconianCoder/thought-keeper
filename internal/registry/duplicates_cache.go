package registry

import (
	"context"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// duplicatesTTL is how long one detection serves every caller. The duplicate check is a maintenance
// question, and the PWA asks it twice at start-up (the Status mount and a sync event): without this,
// two full scans ran side by side. The answer is cheap to recompute, and a collapse invalidates it.
const duplicatesTTL = 30 * time.Second

// cachedDuplicates serialises and briefly caches the duplicate detection. The mutex is held across
// the scan on purpose: a second caller waits for the first instead of scanning the same vault again.
type cachedDuplicates struct {
	uc usecases.CollapseDuplicateAtoms

	mu    sync.Mutex
	at    time.Time
	res   partialentities.PartialAtomDuplicates
	valid bool
}

func (c *cachedDuplicates) Detect(ctx context.Context) (partialentities.PartialAtomDuplicates, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && time.Since(c.at) < duplicatesTTL {
		return c.res, nil
	}
	res, err := c.uc.Detect(ctx)
	if err != nil {
		return res, err
	}
	c.res, c.at, c.valid = res, time.Now(), true
	return res, nil
}

func (c *cachedDuplicates) Execute(ctx context.Context) (partialentities.PartialAtomDuplicates, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, err := c.uc.Execute(ctx)
	c.valid = false
	return res, err
}
