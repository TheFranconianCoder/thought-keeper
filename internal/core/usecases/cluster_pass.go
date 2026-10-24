package usecases

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// ClusterPass is the association step: it groups claims into TITLELESS clusters and writes the
// records under clusters/.
//
// There is no model in this step. Membership is a content graph (built by cluster_graph.go), so the
// pass is deterministic and cheap. The only judgement it makes on existing data is the ATTACH step:
// a free claim that is strongly similar to an existing cluster's centroid joins it, instead of
// forming a near-duplicate cluster next to it. Everything else — the graph, the merge/split rules —
// is the algorithm that already produced good groups; the page naming/review stages it used to feed
// are gone because there is no page.
type ClusterPass struct {
	Atoms    interfaces.AtomRepository
	Clusters interfaces.ClusterRepository
	Store    interfaces.ClusterStore
	Settings ClusterSettings
	// Progress is called at the start, after prune/attach, and after the records are written.
	Progress func(ClusterProgress)
	Clock    interfaces.Clock
	Log      *slog.Logger
	// Runs closes the pass's run row. The pass is deterministic, so Model is empty.
	Runs  interfaces.RunRepository
	Model string
}

// ClusterPassResult reports what one pass assigned.
type ClusterPassResult struct {
	Clusters  int
	Atoms     int
	Attached  int
	New       int
	Remaining int
	Complete  bool
	Changed   []ulid.ULID
}

// Execute clusters all claims that no cluster owns yet. It closes its own run row: a pass that
// cannot be watched is a pass nobody can judge.
func (uc ClusterPass) Execute(ctx context.Context, runID ulid.ULID) (res ClusterPassResult, err error) {
	defer func() {
		if uc.Runs == nil {
			return
		}
		status, answer, failure := entities.RunSucceeded, fmt.Sprintf("%d clusters, %d claims (%d attached)", res.New, res.Atoms, res.Attached), ""
		if err != nil {
			status, failure, answer = entities.RunFailed, err.Error(), ""
		}
		_ = uc.Runs.FinishRun(ctx, entities.Run{
			ID: runID, Op: entities.RunPipeline, Step: entities.StepCluster, Model: uc.Model,
			Status: status, Answer: answer, Error: failure,
			StartedAt: uc.now(), EndedAt: uc.now(),
		})
	}()
	if uc.Atoms == nil || uc.Clusters == nil || uc.Store == nil {
		return res, nil
	}
	settings := uc.Settings.withDefaults()
	records, err := uc.Store.ScanMetas(ctx)
	if err != nil {
		return res, fmt.Errorf("cluster pass: scan cluster records: %w", err)
	}
	// The existence set only — the prune answers "does this member still exist", and a settled vault
	// changes no record at all, so its claim texts are never read here.
	existing, err := uc.Atoms.AtomIDs(ctx)
	if err != nil {
		return res, fmt.Errorf("cluster pass: read claims: %w", err)
	}
	exists := make(map[ulid.ULID]bool, len(existing))
	for _, id := range existing {
		exists[id] = true
	}
	total, err := uc.Atoms.AtomCount(ctx)
	if err != nil {
		return res, fmt.Errorf("cluster pass: count claims: %w", err)
	}

	progress := ClusterProgress{Phase: "attach", ClaimsTotal: total}
	uc.report(progress)

	// 1. Reconcile: a cluster is a cache over claims that exist. A member that vanished is dropped
	//    and a cluster that loses all of them is removed. This is the whole "dirty" story now.
	records, changedRecords, err := uc.prune(ctx, records, exists)
	if err != nil {
		return res, err
	}
	resultChanged := append([]ulid.ULID(nil), changedRecords...)

	// 2. Attach free claims to existing clusters by centroid similarity. The whole-vault statistics
	//    (document frequencies, centroid sums) and the free set itself come from ONE stream.
	attached, changedByAttach, free, err := uc.attach(ctx, records, settings)
	if err != nil {
		return res, err
	}
	res.Attached = attached
	resultChanged = append(resultChanged, changedByAttach...)
	if attached > 0 {
		// Recompute free: the attach step returned the clusters it grew.
		owned := map[ulid.ULID]bool{}
		for _, rec := range records {
			for _, id := range rec.Atoms {
				owned[id] = true
			}
		}
		remaining := make([]entities.Atom, 0, len(free))
		for _, a := range free {
			if !owned[a.ID] {
				remaining = append(remaining, a)
			}
		}
		free = remaining
	}
	res.Atoms = attached
	progress.ClaimsDone = attached
	progress.Attached = attached
	progress.Remaining = len(free)
	if len(free) == 0 {
		res.Complete = true
		res.Clusters = len(resultChanged)
		res.Changed = uniqueULIDs(resultChanged)
		progress.Phase = "done"
		progress.Complete = true
		progress.Clusters = len(records)
		uc.report(progress)
		return res, nil
	}
	// The graph seeds groups from outline nodes, so the claims it will group need their provenance.
	// Exactly these claims — one query, not a whole-corpus read.
	ids := make([]ulid.ULID, 0, len(free))
	for i := range free {
		ids = append(ids, free[i].ID)
	}
	prov, err := uc.Atoms.SourcesForAtoms(ctx, ids)
	if err != nil {
		return res, fmt.Errorf("cluster pass: read provenance: %w", err)
	}
	for i := range free {
		free[i].Sources = prov[free[i].ID]
	}

	// 3. Graph the claims that no cluster could take, split what has several cores, and write each
	//    group as a new cluster record.
	progress.Phase = "cluster"
	progress.TopicsTotal = topicCount(free)
	uc.report(progress)
	if uc.Log != nil {
		uc.Log.Info("cluster graph built", "free_claims", len(free), "topics", progress.TopicsTotal,
			"existing_clusters", len(records), "attached", attached)
	}
	g := buildClaimGraph(free, settings)
	candidates := g.candidates()
	assignments := make([]clusterAssignment, 0, len(candidates))
	for i := range candidates {
		assignments = append(assignments, clusterAssignment{ID: candidates[i].ID, Members: append([]int(nil), candidates[i].Members...)})
	}
	// A claim with real edges into several cores may belong to several clusters; the graph already
	// carries those edges, so the overlap is read from them and never from the topic.
	assignments = addOverlap(assignments, g, settings)
	progress.TopicsDone = progress.TopicsTotal
	progress.Clusters = len(assignments)
	uc.report(progress)

	newClusters, assigned, err := uc.writeCandidates(ctx, assignments, g, settings, atomsByIDFrom(free))
	if err != nil {
		return res, err
	}
	resultChanged = append(resultChanged, newClusters...)
	res.New = len(newClusters)
	res.Clusters = len(newClusters) + len(changedByAttach)
	res.Atoms += len(assigned)
	res.Changed = uniqueULIDs(resultChanged)
	res.Remaining = 0
	for _, a := range free {
		if !assigned[a.ID] {
			res.Remaining++
		}
	}
	res.Complete = res.Remaining == 0
	progress.ClaimsDone = res.Atoms
	progress.Remaining = res.Remaining
	progress.New = res.New
	progress.Clusters = len(records) + res.New
	progress.Complete = res.Complete
	progress.Phase = "done"
	uc.report(progress)
	if uc.Log != nil {
		uc.Log.Info("cluster pass finished", "new_clusters", res.New, "attached", res.Attached,
			"atoms", res.Atoms, "remaining", res.Remaining, "complete", res.Complete)
	}
	return res, nil
}

func (uc ClusterPass) report(p ClusterProgress) {
	if uc.Progress != nil {
		uc.Progress(p)
	}
}

// prune drops members that no longer exist and removes clusters that lose them all.
//
// It reads no claim text for the common case: existence is the id set, and a record whose members
// all survive is touched by nobody. Only records that ACTUALLY lost a member need their survivors'
// text for the signature — and they are fetched in one query, after the scan classified them.
func (uc ClusterPass) prune(ctx context.Context, records []entities.Cluster, exists map[ulid.ULID]bool) ([]entities.Cluster, []ulid.ULID, error) {
	// First pass: classify, collecting the ids whose text the rewrite of a changed record needs.
	var lost, changes []entities.Cluster
	var keptOf [][]ulid.ULID
	need := make([]ulid.ULID, 0, 8)
	for _, rec := range records {
		kept := make([]ulid.ULID, 0, len(rec.Atoms))
		for _, id := range rec.Atoms {
			if exists[id] {
				kept = append(kept, id)
			}
		}
		switch {
		case len(kept) == len(rec.Atoms):
			continue // nothing was lost: the record stands untouched
		case len(kept) == 0:
			lost = append(lost, rec)
		default:
			rec.Atoms = kept
			changes = append(changes, rec)
			keptOf = append(keptOf, kept)
			need = append(need, kept...)
		}
	}
	if len(lost) == 0 && len(changes) == 0 {
		return records, nil, nil
	}

	var byID map[string]entities.Atom
	if len(changes) > 0 {
		texts, err := uc.Atoms.AtomTextsForIDs(ctx, uniqueULIDs(need))
		if err != nil {
			return nil, nil, fmt.Errorf("cluster pass: read pruned members: %w", err)
		}
		byID = make(map[string]entities.Atom, len(texts))
		for _, a := range texts {
			byID[a.ID.String()] = a
		}
	}

	// Second pass: the writes, in the records' own order — the same order the per-record loop had
	// before this split.
	now := uc.now()
	out := make([]entities.Cluster, 0, len(records))
	changed := make([]ulid.ULID, 0, len(lost)+len(changes))
	changeAt := 0
	for _, rec := range records {
		kept := make([]ulid.ULID, 0, len(rec.Atoms))
		for _, id := range rec.Atoms {
			if exists[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) == len(rec.Atoms) {
			out = append(out, rec)
			continue
		}
		if len(kept) == 0 {
			if err := uc.Store.RemoveMeta(ctx, rec.ID); err != nil {
				return nil, nil, fmt.Errorf("cluster pass: remove empty cluster: %w", err)
			}
			_ = uc.Clusters.DeleteCluster(ctx, rec.ID)
			changed = append(changed, rec.ID)
			continue
		}
		rec.Atoms = keptOf[changeAt]
		changeAt++
		rec.Signature = subjectSignature(atomsFor(kept, byID))
		rec.Updated = now
		if err := uc.Store.WriteMeta(ctx, rec); err != nil {
			return nil, nil, fmt.Errorf("cluster pass: rewrite pruned cluster: %w", err)
		}
		if err := uc.Clusters.SaveCluster(ctx, rec); err != nil {
			return nil, nil, err
		}
		out = append(out, rec)
		changed = append(changed, rec.ID)
	}
	return out, changed, nil
}

// centroidTerm is one term of one member's contribution to a cluster's centroid sum, kept in the
// arrival order of the stream: the sum is taken over these in exactly the order the whole-corpus
// build added them (claims by id, terms by interned id), so the weights are the same bits.
type centroidTerm struct {
	term int32
	tf   float32 // term weights are multiples of 0.25 — exact in a float32
}

// attach joins free claims to the existing cluster whose centroid they resemble. It is the inverse
// of the old title-based update: with no titles, content similarity is the only evidence that a new
// claim belongs to an already-derived group.
//
// The whole-vault statistics come from ONE stream over the claim texts: document frequencies for the
// IDF, the (term, tf) contributions of every owned claim for the centroid sums, and the free set
// itself for the caller. Nothing else is kept — the pass used to materialise a corpus of every
// claim to place a handful of new ones.
func (uc ClusterPass) attach(ctx context.Context, records []entities.Cluster, settings ClusterSettings) (int, []ulid.ULID, []entities.Atom, error) {
	owner := make(map[ulid.ULID][]int)
	for ci, rec := range records {
		for _, id := range rec.Atoms {
			owner[id] = append(owner[id], ci)
		}
	}
	scoring := len(records) > 0 && settings.AttachMinScore > 0

	var (
		tokenizer termScratch
		counts    = map[string]float64{}
		scratch   []int32
		ct        []centroidTerm
		terms     []string
		index     = map[string]int32{}
		df        []int32
		free      = make([]entities.Atom, 0, 64)
		entries   [][]centroidTerm
		total     int
	)
	if scoring {
		entries = make([][]centroidTerm, len(records))
	}
	streamErr := uc.Atoms.StreamAtomTexts(ctx, func(a entities.Atom) error {
		total++
		_, owned := owner[a.ID]
		if !scoring {
			if !owned {
				free = append(free, a)
			}
			return nil
		}
		clear(counts)
		addWeightedTerms(counts, a.Claim, 2.0, &tokenizer)
		addWeightedTerms(counts, a.Body, 1.0, &tokenizer)
		addWeightedTerms(counts, a.Topic, 0.25, &tokenizer)
		scratch = scratch[:0]
		for term := range counts {
			id, ok := index[term]
			if !ok {
				id = int32(len(terms))
				terms = append(terms, term)
				index[term] = id
				df = append(df, 0)
			}
			scratch = append(scratch, id)
		}
		slices.Sort(scratch)
		for _, id := range scratch {
			df[id]++
		}
		if owned {
			ct = ct[:0]
			for _, id := range scratch {
				ct = append(ct, centroidTerm{term: id, tf: float32(counts[terms[id]])})
			}
			for _, ci := range owner[a.ID] {
				entries[ci] = append(entries[ci], ct...)
			}
		} else {
			// Free: it fed the document frequencies, but no cluster owns it — it is one of the
			// claims the pass must place.
			free = append(free, a)
		}
		return nil
	})
	if streamErr != nil {
		return 0, nil, free, streamErr
	}
	if !scoring {
		return 0, nil, free, nil
	}

	// The free claims' vectors are the same IDF numbers the stream computed for the whole corpus —
	// the seed is exactly that: the intern table, the document frequencies, the claim total.
	corpus := newClaimCorpus(free, &corpusSeed{terms: terms, index: index, df: df, total: total})

	// Fold each cluster's (term, tf) entries into its IDF-weighted centroid, in the order the
	// stream arrived — which is the member order the whole-corpus build summed in.
	type centroid struct {
		norm float64
	}
	centroids := make([]centroid, 0, len(records))
	postings := map[int32][]clusterWeight{}
	nPlus1 := float64(total + 1)
	idf := func(term int32) float64 { return math.Log(nPlus1/float64(df[term]+1)) + 0.25 }
	for ci := range records {
		if len(entries[ci]) == 0 {
			centroids = append(centroids, centroid{})
			continue
		}
		vec := centroidVec(entries[ci], idf)
		entries[ci] = nil // one cluster's accumulator is a construction site, not state
		centroids = append(centroids, centroid{norm: math.Sqrt(vec.normSq())})
		for _, tw := range vec.top(terms, 40) {
			postings[tw.term] = append(postings[tw.term], clusterWeight{cluster: ci, weight: tw.weight})
		}
	}

	now := uc.now()
	grown := make(map[int]bool)
	attached := 0
	for fi, a := range free {
		if err := ctx.Err(); err != nil {
			return attached, nil, free, err
		}
		topIDs, topW := corpus.topTerms(fi)
		scores := map[int]float64{}
		for k, term := range topIDs {
			for _, p := range postings[term] {
				c := centroids[p.cluster]
				if c.norm == 0 {
					continue
				}
				scores[p.cluster] += topW[k] * p.weight
			}
		}
		type match struct {
			cluster int
			score   float64
		}
		matches := make([]match, 0, len(scores))
		for ci, raw := range scores {
			c := centroids[ci]
			if c.norm == 0 || corpus.norm(fi) == 0 {
				continue
			}
			score := raw / (corpus.norm(fi) * c.norm)
			if score >= settings.AttachMinScore {
				matches = append(matches, match{cluster: ci, score: score})
			}
		}
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			return records[matches[i].cluster].ID.String() < records[matches[j].cluster].ID.String()
		})
		if len(matches) > settings.OverlapMax {
			matches = matches[:settings.OverlapMax]
		}
		for _, m := range matches {
			rec := &records[m.cluster]
			rec.Atoms = append(rec.Atoms, a.ID)
			grown[m.cluster] = true
			attached++
		}
	}

	var changed []ulid.ULID
	if len(grown) > 0 {
		// Only GROWN records need member text again, for the signature — and they are a handful,
		// not the vault. The free members are still in this function's own list.
		byClaim := atomsByIDFrom(free)
		ids := make([]ulid.ULID, 0, len(grown)*8)
		for ci := range grown {
			for _, id := range records[ci].Atoms {
				if _, ok := byClaim[id.String()]; !ok {
					ids = append(ids, id)
				}
			}
		}
		texts, err := uc.Atoms.AtomTextsForIDs(ctx, uniqueULIDs(ids))
		if err != nil {
			return attached, nil, free, fmt.Errorf("cluster pass: read grown members: %w", err)
		}
		for _, a := range texts {
			byClaim[a.ID.String()] = a
		}
		for ci := range grown {
			rec := records[ci]
			rec.Atoms = uniqueULIDs(rec.Atoms)
			rec.Signature = subjectSignature(atomsFor(rec.Atoms, byClaim))
			rec.Updated = now
			if err := uc.Store.WriteMeta(ctx, rec); err != nil {
				return attached, nil, free, fmt.Errorf("cluster pass: write attached cluster: %w", err)
			}
			if err := uc.Clusters.SaveCluster(ctx, rec); err != nil {
				return attached, nil, free, err
			}
			changed = append(changed, rec.ID)
		}
	}
	return attached, changed, free, nil
}

// clusterAssignment is one group the graph produced, in the form the overlap pass and the writer
// need. Title exists only so a test (or a log line) can name a group; a cluster itself has none.
type clusterAssignment struct {
	ID      string
	Title   string
	Members []int
}

// addOverlap gives a claim a second cluster when it has real edges into that cluster's core. It reads
// the GRAPH, never the topic: shared words are evidence, a shared subject name is not.
func addOverlap(assignments []clusterAssignment, g *claimGraph, settings ClusterSettings) []clusterAssignment {
	if len(assignments) < 2 || settings.OverlapMax < 2 || g.corpus == nil || g.corpus.size() < len(g.atoms) {
		return assignments
	}
	minEdges := settings.OverlapMinEdges
	if minEdges <= 0 {
		minEdges = 4
	}
	maxMembers := settings.MaxClusterClaims
	if maxMembers <= 0 {
		maxMembers = 80
	}

	owner := map[int][]int{}
	primary := make([]map[int]bool, len(assignments))
	sums := make([]sparseVec, len(assignments))
	normSq := make([]float64, len(assignments))
	for i, assignment := range assignments {
		primary[i] = make(map[int]bool, len(assignment.Members))
		for _, member := range assignment.Members {
			owner[member] = append(owner[member], i)
			primary[i][member] = true
			sums[i].mergeInto(g.corpus.normalizedVec(member))
		}
		normSq[i] = sums[i].normSq()
	}

	extra := make([]map[int]bool, len(assignments))
	for member, primaryClusters := range owner {
		if len(primaryClusters) >= settings.OverlapMax {
			continue
		}
		neighborCount := len(g.neighbors[member].at)
		if neighborCount < minEdges {
			continue
		}
		counts := map[int]int{}
		for _, neighbor := range g.neighbors[member].at {
			for _, cluster := range owner[int(neighbor)] {
				counts[cluster]++
			}
		}
		type candidate struct {
			cluster int
			count   int
		}
		list := make([]candidate, 0, len(counts))
		for cluster, count := range counts {
			list = append(list, candidate{cluster: cluster, count: count})
		}
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].count != list[j].count {
				return list[i].count > list[j].count
			}
			return list[i].cluster < list[j].cluster
		})
		chosen := append([]int(nil), primaryClusters...)
		for _, item := range list {
			if len(chosen) >= settings.OverlapMax {
				break
			}
			if item.cluster >= len(assignments) {
				continue
			}
			// A second membership must be visible as several real edges AND make up a meaningful
			// share of the claim's neighbourhood. A single shared generic term is not a bridge.
			if item.count < minEdges || item.count*4 < neighborCount {
				continue
			}
			if len(assignments[item.cluster].Members)+len(extra[item.cluster]) >= maxMembers {
				continue
			}
			// The same content test as attach: the claim must be near the other cluster's centroid,
			// not merely adjacent to two of its claims.
			target := sums[item.cluster]
			if normSq[item.cluster] <= 0 || g.corpus.norm(member) <= 0 {
				continue
			}
			cosine := g.corpus.dotNormalizedVec(member, target) / (g.corpus.norm(member) * math.Sqrt(normSq[item.cluster]))
			if cosine < settings.AttachMinScore {
				continue
			}
			already := slices.Contains(chosen, item.cluster)
			if already {
				continue
			}
			if extra[item.cluster] == nil {
				extra[item.cluster] = map[int]bool{}
			}
			extra[item.cluster][member] = true
			chosen = append(chosen, item.cluster)
		}
	}

	// The size cap is enforced AFTER overlap. Before it, `splitOversized` guaranteed it for the
	// primary groups; adding bridges must never turn a good cluster into a 300-claim bucket.
	for i := range assignments {
		if len(extra[i]) == 0 {
			continue
		}
		members := make([]int, 0, len(assignments[i].Members)+len(extra[i]))
		for _, member := range assignments[i].Members {
			if primary[i][member] {
				members = append(members, member)
			}
		}
		extras := make([]int, 0, len(extra[i]))
		for member := range extra[i] {
			extras = append(extras, member)
		}
		sort.Ints(extras)
		for _, member := range extras {
			if len(members) >= maxMembers {
				break
			}
			members = append(members, member)
		}
		sort.Ints(members)
		assignments[i].Members = members
	}
	return assignments
}

// writeCandidates writes one cluster record per graph candidate (plus the overlap memberships) and
// returns the record ids and the assigned claim ids.
func (uc ClusterPass) writeCandidates(ctx context.Context, candidates []clusterAssignment, g *claimGraph, settings ClusterSettings, atomsByID map[string]entities.Atom) ([]ulid.ULID, map[ulid.ULID]bool, error) {
	now := uc.now()
	assigned := map[ulid.ULID]bool{}
	changed := make([]ulid.ULID, 0, len(candidates))
	for ci := range candidates {
		if err := ctx.Err(); err != nil {
			return changed, assigned, err
		}
		c := &candidates[ci]
		atomIDs := make([]ulid.ULID, 0, len(c.Members))
		for _, member := range c.Members {
			id := g.atoms[member].ID
			atomIDs = append(atomIDs, id)
			assigned[id] = true
		}
		rec := entities.Cluster{
			ID:      ulid.Make(),
			Atoms:   uniqueULIDs(atomIDs),
			Created: now,
			Updated: now,
		}
		rec.Signature = subjectSignature(atomsFor(rec.Atoms, atomsByID))
		if err := uc.Store.WriteMeta(ctx, rec); err != nil {
			return changed, assigned, fmt.Errorf("cluster pass: write cluster: %w", err)
		}
		if err := uc.Clusters.SaveCluster(ctx, rec); err != nil {
			return changed, assigned, err
		}
		changed = append(changed, rec.ID)
	}
	return changed, assigned, nil
}

// clusterWeight is one term of a cluster centroid in the postings index: which cluster carries it
// and with what weight.
type clusterWeight struct {
	cluster int
	weight  float64
}

func atomsByIDFrom(atoms []entities.Atom) map[string]entities.Atom {
	out := make(map[string]entities.Atom, len(atoms))
	for _, a := range atoms {
		out[a.ID.String()] = a
	}
	return out
}

func (uc ClusterPass) now() time.Time {
	if uc.Clock != nil {
		return uc.Clock.Now()
	}
	return time.Now().UTC()
}
