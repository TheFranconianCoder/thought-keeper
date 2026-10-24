//go:build probe

// Temporary measurement harness for the deterministic cluster phase against a
// copy of a real index.db. Not part of the suite; delete after use.
package usecases

import (
	"context"
	"database/sql"
	"os"
	"runtime"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
	"github.com/oklog/ulid/v2"
)

func TestProbeDeterministicGraph(t *testing.T) {
	path := os.Getenv("TK_LIVE_DB")
	if path == "" {
		t.Skip("set TK_LIVE_DB")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, topic, claim, body FROM atoms`)
	if err != nil {
		t.Fatal(err)
	}
	var atoms []entities.Atom
	byID := map[string]int{}
	for rows.Next() {
		var id []byte
		var topic, claim, body string
		if err := rows.Scan(&id, &topic, &claim, &body); err != nil {
			t.Fatal(err)
		}
		a := entities.Atom{ID: ulid.ULID(id), Topic: topic, Claim: claim, Body: body}
		byID[a.ID.String()] = len(atoms)
		atoms = append(atoms, a)
	}
	rows.Close()

	if sr, err := db.Query(`SELECT atom_id, source_id, node_id FROM atom_sources WHERE node_id <> ''`); err == nil {
		for sr.Next() {
			var aid, sid []byte
			var node string
			if err := sr.Scan(&aid, &sid, &node); err == nil {
				if i, ok := byID[ulid.ULID(aid).String()]; ok {
					atoms[i].Sources = append(atoms[i].Sources, entities.AtomSource{
						Hash: ulid.ULID(sid).String(), NodeID: node,
					})
				}
			}
		}
		sr.Close()
	}

	start := time.Now()
	g := buildClaimGraph(atoms, ClusterSettings{})
	build := time.Since(start)
	start = time.Now()
	candidates := g.candidates()
	cand := time.Since(start)

	var tiny, small, over80, broad int
	maxClaims, maxTopics := 0, 0
	edgeTotal := 0
	for _, ns := range g.neighbors {
		edgeTotal += len(ns.at)
	}
	for _, c := range candidates {
		switch {
		case len(c.Members) == 1:
			tiny++
		case len(c.Members) <= 3:
			small++
		}
		if c.Metrics.Topics >= 12 {
			broad++
		}
		if c.Metrics.Claims > maxClaims {
			maxClaims = c.Metrics.Claims
		}
		if c.Metrics.Topics > maxTopics {
			maxTopics = c.Metrics.Topics
		}
		if c.Metrics.Claims > 80 {
			over80++
		}
	}
	t.Logf("PROBE claims=%d build=%s candidates=%s count=%d edges=%d "+
		"max_claims=%d max_topics=%d tiny_1=%d small_2_3=%d topics12=%d over80=%d",
		len(atoms), build.Round(time.Millisecond), cand.Round(time.Millisecond),
		len(candidates), edgeTotal, maxClaims, maxTopics, tiny, small, broad, over80)
}

// TestProbeDuplicateDetect measures the duplicate check against a real index. It used to decode
// every atom FILE (a ~770 MB spike at start-up, asked twice); the check now streams the index.
func TestProbeDuplicateDetect(t *testing.T) {
	path := os.Getenv("TK_LIVE_DB")
	if path == "" {
		t.Skip("set TK_LIVE_DB")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := sqlc.NewAtomRepository(db)
	uc := CollapseDuplicateAtoms{AtomRepo: repo}

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	res, err := uc.Detect(context.Background())
	dur := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("PROBE duplicates groups=%d files=%d dur=%s heap_delta=%.0fMB",
		res.Groups, res.Files, dur.Round(time.Millisecond), float64(after.HeapAlloc-before.HeapAlloc)/(1<<20))
}
