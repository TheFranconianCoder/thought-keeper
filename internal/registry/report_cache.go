package registry

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errReportSuperseded is the answer of a cold read whose scan the vault changed under, three times
// in a row. It is a last resort: a discarded scan is normally simply asked again.
var errReportSuperseded = errors.New("the report was discarded while the vault changed")

// reportCache serves one computed report to every reader and keeps it fresh behind them.
//
// The Status lens asks for the vocabulary health and the duplicate scan on entry. Both are full
// vault scans (~400 ms and ~1 s on a six-figure vault), and computing them inside the request is
// what made the page wait — twice, because the lens and the catch-up both ask. A reader now gets
// the last result at once; a result older than the TTL is refreshed in the background. Only a cache
// that has never filled makes a caller wait, and then exactly one build runs while the others join
// it. The build is shared, so it is not tied to the caller that started it.
type reportCache[T any] struct {
	compute func(context.Context) (T, error)
	ttl     time.Duration

	mu    sync.Mutex
	value T
	err   error
	at    time.Time
	valid bool
	gen   int
	build *reportBuild
}

// reportBuild is one in-flight computation. Its channel closes when the value or the error is set.
type reportBuild struct{ done chan struct{} }

// Get returns the cached report. A stale report is served as it is and refreshed behind the reader;
// a report that has never filled is built once, and every caller waiting for it shares the result.
func (c *reportCache[T]) Get(ctx context.Context) (T, error) {
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		if c.valid {
			value, stale := c.value, time.Since(c.at) >= c.ttl
			if stale {
				c.startLocked()
			}
			c.mu.Unlock()
			return value, nil
		}
		build := c.build
		if build == nil {
			build = c.startLocked()
		}
		c.mu.Unlock()

		// The first reader waits for the one build; the rest of the app keeps reading the last answer.
		select {
		case <-build.done:
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}

		c.mu.Lock()
		if c.valid {
			value := c.value
			c.mu.Unlock()
			return value, nil
		}
		err := c.err
		c.mu.Unlock()
		// A failure is the answer. A build the vault changed under is asked again — a collapse can
		// land while the first scan runs.
		if err != nil {
			var zero T
			return zero, err
		}
		if attempt >= 2 {
			var zero T
			return zero, errReportSuperseded
		}
	}
}

// Warm builds the first report and returns when it is in the cache, so a start does not pay the
// scan on the first visit to Status. Callers that must not block run it on their own goroutine.
func (c *reportCache[T]) Warm() {
	c.mu.Lock()
	if c.valid {
		c.mu.Unlock()
		return
	}
	build := c.startLocked()
	c.mu.Unlock()
	<-build.done
}

// Invalidate drops the cached report; the next reader builds it again. A build that was already
// running when the vault changed is discarded instead of filling the cache with the old vault.
func (c *reportCache[T]) Invalidate() {
	c.mu.Lock()
	c.valid = false
	c.gen++
	c.mu.Unlock()
}

// startLocked launches the one in-flight build. The caller holds c.mu.
func (c *reportCache[T]) startLocked() *reportBuild {
	if c.build != nil {
		return c.build
	}
	build := &reportBuild{done: make(chan struct{})}
	c.build = build
	gen := c.gen
	// The computation is shared, so it is not tied to the caller that happened to start it: a
	// client that navigates away must not cancel the report every other window is waiting for.
	go func() {
		value, err := c.compute(context.Background())
		c.mu.Lock()
		if gen == c.gen {
			if err == nil {
				c.value, c.at, c.valid, c.err = value, time.Now(), true, nil
			} else {
				c.err = err
			}
		}
		c.build = nil
		c.mu.Unlock()
		close(build.done)
	}()
	return build
}
