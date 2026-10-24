// Package clusters stores the association layer of the pipeline: one JSON record per cluster under
// the vault's `clusters/` directory, addressed by the cluster's ULID and sharded two levels deep by
// the tail of that ULID — the exact rule atoms use, because a ULID's head is a timestamp and
// sharding by it would pile every cluster of one pass into a single folder.
//
// A record is a CACHE (the claims are the truth and the graph over them is deterministic), so this
// package has no page codec, no dirty markers, no generated index and no log. Re-deriving the
// whole directory from the claims must always be enough.
package clusters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/idpath"
)

// Store is the cluster record store rooted at the vault's clusters/ directory.
type Store struct {
	root string
}

// New returns a store rooted at root.
func New(root string) *Store { return &Store{root: root} }

// Root is the clusters directory this store works in.
func (s *Store) Root() string { return s.root }

// RecordPath is the vault-relative record of a cluster: <ab>/<cd>/<id>.meta. The cluster is
// addressed by its identity only — it has no title, so nothing else could name the path.
func RecordPath(id ulid.ULID) string {
	return filepath.ToSlash(filepath.Join(idpath.Shard(id), id.String()+".meta"))
}

// Path is the vault-relative record path for a store caller.
func (s *Store) Path(id ulid.ULID) string { return RecordPath(id) }

// record is the on-disk shape of a cluster. It stays a separate type so the file format cannot
// drift with the domain entity's Go shape.
type record struct {
	ID        string    `json:"id"`
	Atoms     []string  `json:"atoms"`
	Signature string    `json:"signature,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// WriteMeta stores a cluster record atomically (temp → rename, the vault's one-writer rule).
func (s *Store) WriteMeta(_ context.Context, c entities.Cluster) error {
	if c.ID == (ulid.ULID{}) {
		return fmt.Errorf("clusters: a cluster needs an id")
	}
	if c.Updated.IsZero() {
		c.Updated = time.Now().UTC()
	}
	r := record{ID: c.ID.String(), Atoms: make([]string, 0, len(c.Atoms)), Signature: c.Signature, Updated: c.Updated.UTC()}
	for _, a := range c.Atoms {
		r.Atoms = append(r.Atoms, a.String())
	}
	if !c.Created.IsZero() {
		r.Created = c.Created.UTC()
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(s.root, RecordPath(c.ID))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadMeta loads one cluster record.
func (s *Store) ReadMeta(_ context.Context, id ulid.ULID) (entities.Cluster, error) {
	b, err := os.ReadFile(filepath.Join(s.root, RecordPath(id)))
	if err != nil {
		return entities.Cluster{}, err
	}
	return decode(id, b)
}

// ScanMetas returns every cluster record, sorted by id (stable re-derivation input).
func (s *Store) ScanMetas(ctx context.Context) ([]entities.Cluster, error) {
	if _, err := os.Stat(s.root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var paths []string
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if d.IsDir() {
			if path != s.root && d.Name() == ".trash" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(d.Name()) != ".meta" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("clusters: scan records: %w", err)
	}
	if len(paths) == 0 {
		return nil, nil
	}

	// The start-up consistency check reads EVERY record, and a grown vault holds 13k small files:
	// a serial walk made it the slowest thing a start does. The reads are independent, so they run
	// in parallel; the result is sorted by id afterwards, exactly like the serial walk was, so the
	// order a caller sees does not depend on scheduling.
	const workers = 8
	out := make([]entities.Cluster, len(paths))
	ok := make([]bool, len(paths))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				b, rErr := os.ReadFile(paths[i])
				if rErr != nil {
					continue // one unreadable record must not fail the whole scan
				}
				name := filepath.Base(paths[i])
				id, pErr := ulid.ParseStrict(name[:len(name)-len(".meta")])
				if pErr != nil {
					continue
				}
				c, dErr := decode(id, b)
				if dErr != nil || c.ID == (ulid.ULID{}) {
					continue
				}
				out[i], ok[i] = c, true
			}
		})
	}
	for i := range paths {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	records := out[:0]
	for i := range out {
		if ok[i] {
			records = append(records, out[i])
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID.String() < records[j].ID.String() })
	return records, nil
}

// RemoveMeta deletes one cluster record. The record is a cache: an empty shard folder left behind
// is swept on the next full re-derivation, so removing never has to prune directories itself.
func (s *Store) RemoveMeta(_ context.Context, id ulid.ULID) error {
	path := filepath.Join(s.root, RecordPath(id))
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// decode turns one record file into the domain cluster.
func decode(id ulid.ULID, b []byte) (entities.Cluster, error) {
	var r record
	if err := json.Unmarshal(b, &r); err != nil {
		return entities.Cluster{}, err
	}
	c := entities.Cluster{ID: id, Signature: r.Signature, Created: r.Created, Updated: r.Updated}
	if idText, perr := ulid.Parse(r.ID); perr == nil {
		c.ID = idText
	}
	for _, a := range r.Atoms {
		parsed, perr := ulid.Parse(a)
		if perr != nil {
			continue
		}
		c.Atoms = append(c.Atoms, parsed)
	}
	return c, nil
}

// The store must satisfy the core port — checked at compile time, not when the registry wires it.
var _ interfaces.ClusterStore = (*Store)(nil)
