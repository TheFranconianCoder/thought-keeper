package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// cleanupAtoms is the existence set the pass reads, plus the text of the members a pruned record
// keeps. The embedded interface supplies the methods it never calls.
type cleanupAtoms struct {
	interfaces.AtomRepository
	ids   []ulid.ULID
	texts []entities.Atom
}

func (a *cleanupAtoms) AtomIDs(context.Context) ([]ulid.ULID, error) { return a.ids, nil }

func (a *cleanupAtoms) AtomTextsForIDs(context.Context, []ulid.ULID) ([]entities.Atom, error) {
	return a.texts, nil
}

type cleanupStore struct {
	interfaces.ClusterStore
	records   []entities.Cluster
	removed   []ulid.ULID
	written   []entities.Cluster
	removeErr error
	writeErr  error
}

func (s *cleanupStore) ScanMetas(context.Context) ([]entities.Cluster, error) {
	return s.records, nil
}

func (s *cleanupStore) RemoveMeta(_ context.Context, id ulid.ULID) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	s.removed = append(s.removed, id)
	return nil
}

func (s *cleanupStore) WriteMeta(_ context.Context, c entities.Cluster) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.written = append(s.written, c)
	return nil
}

type cleanupRepo struct {
	interfaces.ClusterRepository
	deleted   []ulid.ULID
	saved     []entities.Cluster
	deleteErr error
	saveErr   error
}

func (r *cleanupRepo) DeleteCluster(_ context.Context, id ulid.ULID) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *cleanupRepo) SaveCluster(_ context.Context, c entities.Cluster) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, c)
	return nil
}

// TestCleanupLeavesAConsistentLayerAlone: a record whose members all exist is not touched at all.
func TestCleanupLeavesAConsistentLayerAlone(t *testing.T) {
	member := ulid.Make()
	store := &cleanupStore{records: []entities.Cluster{{ID: ulid.Make(), Atoms: []ulid.ULID{member}}}}
	repo := &cleanupRepo{}
	uc := Maintenance{Atoms: &cleanupAtoms{ids: []ulid.ULID{member}}, ClusterStore: store, ClusterRepo: repo}

	res, err := uc.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(store.written) != 0 || len(store.removed) != 0 || len(repo.saved) != 0 || len(repo.deleted) != 0 {
		t.Fatalf("a consistent layer was rewritten: written=%d removed=%d saved=%d deleted=%d",
			len(store.written), len(store.removed), len(repo.saved), len(repo.deleted))
	}
	if res.ClustersPruned != 0 || res.ClustersRemoved != 0 {
		t.Fatalf("res = %+v, want nothing pruned", res)
	}
}

// TestCleanupFailsWhenTheEmptyClusterCannotBeDeleted: the record is gone and the row is not — the
// pass must say so, because that disagreement is what triggers a full index rebuild on the next
// start.
func TestCleanupFailsWhenTheEmptyClusterCannotBeDeleted(t *testing.T) {
	store := &cleanupStore{records: []entities.Cluster{{ID: ulid.Make(), Atoms: []ulid.ULID{ulid.Make()}}}}
	repo := &cleanupRepo{deleteErr: errors.New("database is locked")}
	uc := Maintenance{Atoms: &cleanupAtoms{}, ClusterStore: store, ClusterRepo: repo}

	if _, err := uc.Cleanup(context.Background()); err == nil {
		t.Fatal("cleanup swallowed a failed cluster delete")
	}
}

// TestCleanupFailsWhenThePrunedClusterCannotBeIndexed: the record now carries fewer members and the
// row does not. Returned, not logged: the caller has to know the layer is half-written.
func TestCleanupFailsWhenThePrunedClusterCannotBeIndexed(t *testing.T) {
	kept := ulid.Make()
	store := &cleanupStore{records: []entities.Cluster{{
		ID: ulid.Make(), Atoms: []ulid.ULID{kept, ulid.Make()},
	}}}
	repo := &cleanupRepo{saveErr: errors.New("database is locked")}
	uc := Maintenance{
		Atoms:        &cleanupAtoms{ids: []ulid.ULID{kept}, texts: []entities.Atom{{ID: kept, Claim: "kept"}}},
		ClusterStore: store, ClusterRepo: repo,
	}

	if _, err := uc.Cleanup(context.Background()); err == nil {
		t.Fatal("cleanup swallowed a failed cluster save")
	}
	if len(store.written) != 1 {
		t.Fatalf("written records = %d, want the pruned record before the failed save", len(store.written))
	}
}

// TestCleanupFailsWhenTheEmptyRecordCannotBeRemoved: the record is still there, so the row must stay
// too — and the pass reports the failure instead of pretending it pruned.
func TestCleanupFailsWhenTheEmptyRecordCannotBeRemoved(t *testing.T) {
	store := &cleanupStore{
		records:   []entities.Cluster{{ID: ulid.Make(), Atoms: []ulid.ULID{ulid.Make()}}},
		removeErr: errors.New("read-only file system"),
	}
	repo := &cleanupRepo{}
	uc := Maintenance{Atoms: &cleanupAtoms{}, ClusterStore: store, ClusterRepo: repo}

	if _, err := uc.Cleanup(context.Background()); err == nil {
		t.Fatal("cleanup swallowed a failed record removal")
	}
	if len(repo.deleted) != 0 {
		t.Fatal("the row was deleted although its record is still there")
	}
}
