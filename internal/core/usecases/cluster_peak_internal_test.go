package usecases

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// BenchmarkSteadyStatePass is the memory question the vault poses in practice: a settled vault of
// ~170k claims and ~10k clusters, where ONE document run added a handful of new claims. Step 4 must
// attach them, and attaching a claim scores it against every existing cluster — which is why this
// pass reads the whole corpus. The benchmark IS the number that answers "how much does a run cost":
// the peak heap during the pass, not the idle RSS around it.
func BenchmarkSteadyStatePass(b *testing.B) {
	const freeClaims = 135
	settings := DefaultClusterSettings()

	peakHeap(b, func(ctx context.Context) {
		atoms, records, _ := steadyStateVault(b, freeClaims)
		repo := &goldenAtomRepo{atoms: atoms}
		uc := ClusterPass{Atoms: repo, Store: &goldenClusterStore{written: map[string]string{}}, Clusters: &goldenClusterRepo{}}

		// The pass, in the order ClusterPass.Execute runs it.
		attached, _, free, err := uc.attach(ctx, records, settings)
		if err != nil {
			b.Fatal(err)
		}
		owned := map[ulid.ULID]bool{}
		for _, rec := range records {
			for _, id := range rec.Atoms {
				owned[id] = true
			}
		}
		remaining := make([]entities.Atom, 0, freeClaims)
		ids := make([]ulid.ULID, 0, freeClaims)
		for _, a := range free {
			if !owned[a.ID] {
				remaining = append(remaining, a)
				ids = append(ids, a.ID)
			}
		}
		prov, err := repo.SourcesForAtoms(ctx, ids)
		if err != nil {
			b.Fatal(err)
		}
		for i := range remaining {
			remaining[i].Sources = prov[remaining[i].ID]
		}
		g := buildClaimGraph(remaining, settings)
		candidates := g.candidates()
		assignments := make([]clusterAssignment, 0, len(candidates))
		for i := range candidates {
			assignments = append(assignments, clusterAssignment{ID: candidates[i].ID, Members: candidates[i].Members})
		}
		assignments = addOverlap(assignments, g, settings)
		if attached == 0 && len(assignments) == 0 {
			b.Fatal("the steady-state pass did nothing: the fixture is wrong")
		}
	})
}

// steadyStateVault builds a settled vault: every claim but the last freeClaims belongs to a cluster
// record, records are the size step 4 produces (settings.MaxClusterClaims apart, 17-member here to
// keep the fixture ordinary).
func steadyStateVault(b *testing.B, freeClaims int) ([]entities.Atom, []entities.Cluster, []entities.Atom) {
	b.Helper()
	const size = 170_000
	atoms := syntheticCorpus(size)
	var seed uint64 = 777
	records := make([]entities.Cluster, 0, size/17)
	for i := 0; i+17 <= size-freeClaims; i += 17 {
		members := make([]ulid.ULID, 0, 17)
		for _, a := range atoms[i : i+17] {
			members = append(members, a.ID)
		}
		records = append(records, entities.Cluster{ID: fixedID(&seed, 60000+i), Atoms: members})
	}
	return atoms, records, atoms[size-freeClaims:]
}

// peakHeap runs the work behind a one-millisecond sampler of the live heap and reports the highest
// value seen. The runtime keeps no peak of its own, and the point of this benchmark is exactly the
// value that is high during a run and gone after it.
func peakHeap(b *testing.B, work func(ctx context.Context)) {
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				observePeak(&peak, ms.HeapAlloc)
				return
			case <-t.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				observePeak(&peak, ms.HeapAlloc)
			}
		}
	}()
	for range b.N {
		b.StopTimer()
		runtime.GC()
		b.StartTimer()
		work(context.Background())
	}
	close(stop)
	<-done
	mb := peak.Load() / (1 << 20)
	b.ReportMetric(float64(mb), "MB-peak")
}

func observePeak(peak *atomic.Uint64, v uint64) {
	for {
		p := peak.Load()
		if v <= p || peak.CompareAndSwap(p, v) {
			return
		}
	}
}
