package clusters

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestScanMetasStaysIdOrdered: the start-up consistency check reads every record, and a grown vault
// holds thousands of files — so the scan reads them in parallel. The order a caller sees must not
// depend on scheduling, and one unreadable record must not fail the whole scan.
func TestScanMetasStaysIdOrdered(t *testing.T) {
	ctx := context.Background()
	store := New(t.TempDir())
	now := time.UnixMilli(1_700_000_000_000).UTC()
	want := make([]ulid.ULID, 0, 40)
	for range 40 {
		c := entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{ulid.Make()}, Created: now, Updated: now}
		if err := store.WriteMeta(ctx, c); err != nil {
			t.Fatalf("write: %v", err)
		}
		want = append(want, c.ID)
	}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })

	// A broken record next to the real ones: skipped, never fatal.
	broken := filepath.Join(store.root, "zz", "zz", "not-a-ulid.meta")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := store.ScanMetas(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("records = %d, want %d (the broken file must be skipped)", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("record %d = %s, want %s (the order must not depend on scheduling)", i, got[i].ID, want[i])
		}
	}
}
