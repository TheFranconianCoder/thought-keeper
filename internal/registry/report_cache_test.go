package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// waitFor polls a condition for a short while. The cache does its work in the background, so a test
// asks "has it happened yet" instead of sleeping a guessed duration.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestReportCacheBuildsOnceForConcurrentReaders: the Status lens and the catch-up ask for the same
// report at the same moment. One build must serve both; a second scan beside the first is exactly
// the pile-up the cache exists to prevent.
func TestReportCacheBuildsOnceForConcurrentReaders(t *testing.T) {
	var (
		mu     sync.Mutex
		builds int
	)
	started := make(chan struct{})
	release := make(chan struct{})
	once := sync.Once{}
	cache := &reportCache[int]{
		ttl: time.Minute,
		compute: func(context.Context) (int, error) {
			mu.Lock()
			builds++
			mu.Unlock()
			once.Do(func() { close(started) })
			<-release
			return 42, nil
		},
	}

	const readers = 8
	results := make(chan int, readers)
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			value, err := cache.Get(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- value
		})
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("get: %v", err)
	}
	got := 0
	for value := range results {
		got++
		if value != 42 {
			t.Errorf("value = %d, want 42", value)
		}
	}
	if got != readers {
		t.Fatalf("readers answered = %d, want %d", got, readers)
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 1 {
		t.Fatalf("builds = %d, want 1", builds)
	}
}

// TestReportCacheServesStaleWhileRefreshing: a reader never waits for a report that exists, even
// when it is older than the TTL. The refresh happens behind the page.
func TestReportCacheServesStaleWhileRefreshing(t *testing.T) {
	var (
		mu      sync.Mutex
		version int
	)
	versioned := func(context.Context) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		version++
		return version, nil
	}
	// A zero TTL means "every answer is stale": the next Get must still answer at once.
	cache := &reportCache[int]{compute: versioned}

	first, err := cache.Get(context.Background())
	if err != nil || first != 1 {
		t.Fatalf("first = (%d, %v), want (1, nil)", first, err)
	}

	second, err := cache.Get(context.Background())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second != 1 {
		t.Fatalf("second = %d, want the cached 1 while the refresh runs", second)
	}

	waitFor(t, "the background refresh", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return version == 2
	})
	// The next read shows the refreshed report.
	waitFor(t, "the refreshed value", func() bool {
		value, err := cache.Get(context.Background())
		return err == nil && value == 2
	})
}

// TestReportCacheRetriesAfterFailure: a build that failed is not a value. The next reader asks
// again instead of being served the failure forever.
func TestReportCacheRetriesAfterFailure(t *testing.T) {
	attempts := 0
	cache := &reportCache[int]{
		ttl: time.Minute,
		compute: func(context.Context) (int, error) {
			attempts++
			if attempts == 1 {
				return 0, errors.New("scan failed")
			}
			return 7, nil
		},
	}
	if _, err := cache.Get(context.Background()); err == nil {
		t.Fatal("first build succeeded, want its error")
	}
	value, err := cache.Get(context.Background())
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if value != 7 {
		t.Fatalf("value = %d, want 7", value)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// TestReportCacheWarm: a start fills the report before anyone asks, so the first Status visit is
// answered from memory.
func TestReportCacheWarm(t *testing.T) {
	var (
		mu     sync.Mutex
		builds int
	)
	cache := &reportCache[int]{
		ttl: time.Minute,
		compute: func(context.Context) (int, error) {
			mu.Lock()
			builds++
			mu.Unlock()
			return 3, nil
		},
	}
	cache.Warm()
	// Warm returns only when the value is in the cache, so the read below cannot start a build.
	value, err := cache.Get(context.Background())
	if err != nil || value != 3 {
		t.Fatalf("get = (%d, %v), want (3, nil)", value, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 1 {
		t.Fatalf("builds = %d, want 1: the warm answer must serve the reader", builds)
	}
}

// TestReportCacheDiscardsABuildTheVaultChangedUnder: a collapse can land while the first scan runs.
// The scan's numbers describe the old vault, so they must not fill the cache.
func TestReportCacheDiscardsABuildTheVaultChangedUnder(t *testing.T) {
	var (
		mu     sync.Mutex
		builds int
	)
	started := make(chan struct{}, 4)
	release := make(chan struct{}, 4)
	cache := &reportCache[int]{
		ttl: time.Minute,
		compute: func(context.Context) (int, error) {
			mu.Lock()
			builds++
			build := builds
			mu.Unlock()
			started <- struct{}{}
			<-release
			return build, nil
		},
	}

	result := make(chan int, 1)
	go func() {
		value, err := cache.Get(context.Background())
		if err != nil {
			result <- -1
			return
		}
		result <- value
	}()

	<-started
	cache.Invalidate()
	release <- struct{}{} // the first scan is discarded
	<-started             // the reader asks again
	release <- struct{}{}

	if got := <-result; got != 2 {
		t.Fatalf("value = %d, want the second scan (2)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 2 {
		t.Fatalf("builds = %d, want 2", builds)
	}
}

// TestReportCacheInvalidateForcesABuild: a collapse changes the vault, so the cached duplicate
// report must not outlive it.
func TestReportCacheInvalidateForcesABuild(t *testing.T) {
	builds := 0
	cache := &reportCache[int]{
		ttl: time.Minute,
		compute: func(context.Context) (int, error) {
			builds++
			return builds, nil
		},
	}
	if _, err := cache.Get(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	cache.Invalidate()
	value, err := cache.Get(context.Background())
	if err != nil {
		t.Fatalf("after invalidate: %v", err)
	}
	if value != 2 {
		t.Fatalf("value = %d, want a rebuild (2)", value)
	}
}
