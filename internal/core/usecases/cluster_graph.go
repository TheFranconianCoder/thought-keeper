package usecases

import (
	"bytes"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// ClusterSettings tunes the deterministic claim graph. The values are QUALITY gates, not target
// sizes: a cluster is one subject, and a broad group is split when its own claims no longer support
// that claim.
type ClusterSettings struct {
	// NeighborK is how many genuinely similar claims one claim is linked to. The graph is sparse:
	// 46k claims must not produce a 46k × 46k matrix.
	NeighborK int
	// MinClaimScore is the smallest IDF-weighted overlap a claim-to-claim edge may have.
	MinClaimScore float64
	// MergeMinInternal is the smallest average cosine to the cluster centroid a group must keep
	// while it grows. A merge that makes the group less coherent than this is refused.
	MergeMinInternal float64
	// MergeMinGap is how much more coherent a group must be internally than it is to its nearest
	// neighbouring group. Without it a group that is merely close to another page is not a subject.
	MergeMinGap float64
	// SplitMinCoreSize is the smallest connected core a split may produce. Tiny fragments are not
	// page-shaped and stay with their parent.
	SplitMinCoreSize int
	// TopicSpanLimit keeps one group from becoming a book of unrelated subjects: a candidate that
	// spans more topics than this is only merged when its internal coherence is clearly good.
	TopicSpanLimit int
	// MaxClusterClaims is the HARD cap of one cluster. Above it the group is split by similarity —
	// not because a big subject is wrong, but because a group this large is usually a failed merge.
	MaxClusterClaims int
	// AttachMinScore is how similar a free claim must be to an existing cluster's centroid (cosine)
	// to join it instead of forming a near-duplicate cluster next to it.
	AttachMinScore float64
	// OverlapMinEdges is how many REAL claim edges a claim needs into another cluster before it may
	// belong to it as well. Two edges were enough to put almost every claim into a second cluster;
	// a bridge has to be visible in the graph, not inferred from a shared word.
	OverlapMinEdges int
	// OverlapMax is how many clusters one claim may belong to when it has real edges into several
	// distinct cores.
	OverlapMax int
	// CrossTopicMinEdges is how many real claim edges two topic blocks need before the first pass
	// may merge them. Topics block the cheap merge; a genuine cross-topic subject still gets through
	// when several claims connect it.
	CrossTopicMinEdges int
}

// DefaultClusterSettings returns the conservative first-run settings: tight clusters first, merge
// later. A false page is more expensive than a too-small page.
func DefaultClusterSettings() ClusterSettings {
	return ClusterSettings{
		NeighborK:          16,
		MinClaimScore:      0.05,
		MergeMinInternal:   0.22,
		MergeMinGap:        0.04,
		SplitMinCoreSize:   3,
		TopicSpanLimit:     12,
		MaxClusterClaims:   80,
		AttachMinScore:     0.18,
		OverlapMinEdges:    4,
		OverlapMax:         2,
		CrossTopicMinEdges: 3,
	}
}

func (s ClusterSettings) withDefaults() ClusterSettings {
	d := DefaultClusterSettings()
	if s.NeighborK <= 0 {
		s.NeighborK = d.NeighborK
	}
	if s.MinClaimScore <= 0 {
		s.MinClaimScore = d.MinClaimScore
	}
	if s.MergeMinInternal <= 0 {
		s.MergeMinInternal = d.MergeMinInternal
	}
	if s.MergeMinGap <= 0 {
		s.MergeMinGap = d.MergeMinGap
	}
	if s.SplitMinCoreSize <= 0 {
		s.SplitMinCoreSize = d.SplitMinCoreSize
	}
	if s.TopicSpanLimit <= 0 {
		s.TopicSpanLimit = d.TopicSpanLimit
	}
	if s.MaxClusterClaims <= 0 {
		s.MaxClusterClaims = d.MaxClusterClaims
	}
	if s.AttachMinScore <= 0 {
		s.AttachMinScore = d.AttachMinScore
	}
	if s.OverlapMinEdges <= 0 {
		s.OverlapMinEdges = d.OverlapMinEdges
	}
	if s.OverlapMax <= 0 {
		s.OverlapMax = d.OverlapMax
	}
	if s.CrossTopicMinEdges <= 0 {
		s.CrossTopicMinEdges = d.CrossTopicMinEdges
	}
	return s
}

// clusterMetrics is what makes a cluster reviewable. None of the values is a target size; they
// describe whether the members actually describe one subject.
type clusterMetrics struct {
	Claims       int
	Topics       int
	TopicEntropy float64
	Internal     float64
	External     float64
	Gap          float64
	CoreTerms    []string
	Cores        int
}

// claimEdge is one content edge of the sparse graph: two claims and the score that connects them.
type claimEdge struct {
	a     int
	b     int
	score float64
}

type claimGraph struct {
	atoms    []entities.Atom
	corpus   *claimCorpus
	edges    []claimEdge
	settings ClusterSettings
	// neighbors is the sparse top-K graph, one sorted slice per claim: a claim's neighbours and
	// their scores. A slice, because 170k small hash maps cost more than the vectors did.
	neighbors []neighborList
	// nodeKeys ties a claim to the source outline node it came from. A shared node is a much
	// stronger block than a shared topic: it is the section the claim was distilled from.
	nodeKeys [][]string
	// topicSizes is how many claims carry a topic. A topic with many claims blocks the cheap
	// cross-topic merge (it is a real subject label); a singleton label does not, because the
	// graph, not a one-claim folder name, must connect it.
	topicSizes map[string]int
}

// neighborList is one claim's neighbourhood: neighbour indices ascending, score per neighbour.
type neighborList struct {
	at    []int32
	score []float64
}

func (l neighborList) scoreOf(other int32) (float64, bool) {
	k := sort.Search(len(l.at), func(x int) bool { return l.at[x] >= other })
	if k >= len(l.at) || l.at[k] != other {
		return 0, false
	}
	return l.score[k], true
}

func (l *neighborList) set(other int32, score float64) {
	k := sort.Search(len(l.at), func(x int) bool { return l.at[x] >= other })
	if k < len(l.at) && l.at[k] == other {
		l.score[k] = score
		return
	}
	l.at = slices.Insert(l.at, k, other)
	l.score = slices.Insert(l.score, k, score)
}

type clusterCandidate struct {
	ID      string
	Members []int
	Metrics clusterMetrics
}

// buildClaimGraph turns claims into a sparse similarity graph. The grouping is deterministic: the
// same claims and settings always produce the same graph and the same candidates.
func buildClaimGraph(atoms []entities.Atom, settings ClusterSettings) *claimGraph {
	settings = settings.withDefaults()
	sorted := append([]entities.Atom(nil), atoms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID.String() < sorted[j].ID.String() })

	corpus := buildClaimCorpus(sorted)
	g := &claimGraph{
		atoms:      sorted,
		corpus:     corpus,
		neighbors:  make([]neighborList, len(sorted)),
		settings:   settings,
		topicSizes: map[string]int{},
		nodeKeys:   make([][]string, len(sorted)),
	}
	for i, atom := range sorted {
		if topic := strings.ToLower(strings.TrimSpace(atom.Topic)); topic != "" {
			g.topicSizes[topic]++
		}
		keys := make([]string, 0, len(atom.Sources))
		for _, src := range atom.Sources {
			if src.Hash != "" && src.NodeID != "" {
				keys = append(keys, src.Hash+"\x1f"+src.NodeID)
			}
		}
		slices.Sort(keys)
		keys = slices.Compact(keys)
		g.nodeKeys[i] = keys
	}
	if len(sorted) == 0 {
		return g
	}

	// A term that occurs in most claims cannot distinguish them, and scanning its whole posting
	// list for every claim is what made the build take minutes. Two bounds make the scan
	// proportional to the claim count instead of to the square of a common term's frequency:
	// a term is capped at postingCap claims, and a term with df above maxDF is dropped entirely.
	// This is the same idea as IDF — a common term carries no signal — applied before the scan.
	const postingCap = 256
	postings := make(map[int32][]int32, corpus.size()*4)
	for i := range sorted {
		ids, _ := corpus.raw(i)
		for _, term := range ids {
			if len(postings[term]) >= postingCap {
				continue // already full: a common term, not worth more candidate work
			}
			postings[term] = append(postings[term], int32(i))
		}
	}
	maxDF := max(len(sorted)/10, 8)

	// Reusable buffers: the per-claim scores map and the per-claim full sort were the two hot
	// allocations. The touched-mark array keeps candidate scoring allocation-free.
	scores := make([]float64, corpus.size())
	touched := make([]int32, 0, 256)
	marked := make([]bool, corpus.size())
	type scored struct {
		index int32
		score float64
	}
	list := make([]scored, 0, 64)

	for i := range sorted {
		touched = touched[:0]
		list = list[:0]
		topIDs, topW := corpus.topTerms(i)
		for k, tw := range topIDs {
			pl := postings[tw]
			if len(pl) == 0 || len(pl) > maxDF {
				continue
			}
			for _, j := range pl {
				if int(j) == i {
					continue
				}
				if !marked[j] {
					marked[j] = true
					touched = append(touched, j)
					scores[j] = 0
				}
				scores[j] += math.Min(topW[k], corpus.weight(int(j), tw))
			}
		}
		// The shared IDF-weighted term mass divided by the smaller claim's mass is an overlap
		// coefficient (0..1): a claim that shares its distinctive terms is similar even when it
		// also says more.
		for _, j := range touched {
			score := scores[j]
			denom := math.Min(corpus.l1(i), corpus.l1(int(j)))
			if denom > 0 {
				score /= denom
			}
			if sorted[i].Topic != "" && sorted[i].Topic == sorted[j].Topic {
				score += 0.02
			}
			if score >= settings.MinClaimScore {
				list = append(list, scored{index: j, score: score})
			}
			marked[j] = false
		}
		if len(list) == 0 {
			continue
		}
		slices.SortFunc(list, func(a, b scored) int {
			if a.score != b.score {
				if a.score > b.score {
					return -1
				}
				return 1
			}
			return bytes.Compare(sorted[a.index].ID[:], sorted[b.index].ID[:])
		})
		if len(list) > settings.NeighborK {
			list = list[:settings.NeighborK]
		}
		// Neighbours are kept in index order: the scans that walk a neighbourhood (the split, the
		// overlap count) then read one array instead of a hash map.
		slices.SortFunc(list, func(a, b scored) int { return int(a.index) - int(b.index) })
		ns := neighborList{at: make([]int32, 0, len(list)), score: make([]float64, 0, len(list))}
		for _, item := range list {
			ns.at = append(ns.at, item.index)
			ns.score = append(ns.score, item.score)
		}
		g.neighbors[i] = ns
	}

	// The top-K relation is not necessarily symmetric. Make it so: a claim that is a strong
	// neighbour of another is a real edge in both directions.
	for i := range g.neighbors {
		at, score := g.neighbors[i].at, g.neighbors[i].score
		for k, j := range at {
			if other, ok := g.neighbors[j].scoreOf(int32(i)); !ok || score[k] > other {
				g.neighbors[j].set(int32(i), score[k])
			}
		}
	}

	seen := map[[2]int32]bool{}
	for i := range g.neighbors {
		at, score := g.neighbors[i].at, g.neighbors[i].score
		for k, j := range at {
			if int(j) == i {
				continue
			}
			a, b := int32(i), j
			if a > b {
				a, b = b, a
			}
			key := [2]int32{a, b}
			if seen[key] {
				continue
			}
			seen[key] = true
			g.edges = append(g.edges, claimEdge{a: int(a), b: int(b), score: score[k]})
		}
	}
	sort.SliceStable(g.edges, func(i, j int) bool {
		if g.edges[i].score != g.edges[j].score {
			return g.edges[i].score > g.edges[j].score
		}
		if g.edges[i].a != g.edges[j].a {
			return g.edges[i].a < g.edges[j].a
		}
		return g.edges[i].b < g.edges[j].b
	})
	return g
}

// termScratch is the tokenizer's two buffers. One association pass reads three texts per claim over
// the whole vault, and a hash map plus a slice per text was the cost of that read; the scratch is
// cleared and reused, so a claim costs no allocation beyond the lowercased text itself.
//
// The returned slice belongs to the scratch: the caller reads it before the next call.
type termScratch struct {
	seen map[string]bool
	out  []string
}

func (s *termScratch) terms(text string) []string {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	clear(s.seen)
	out := s.out[:0]
	add := func(term string) {
		term = strings.Trim(term, "-.:/_")
		if term == "" || s.seen[term] || !acceptableTerm(term) {
			return
		}
		s.seen[term] = true
		out = append(out, term)
	}
	for token := range strings.FieldsFuncSeq(strings.ToLower(text), func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		switch r {
		case '+', '#', '_', ':', '-', '.', '/':
			return false
		default:
			return true
		}
	}) {
		add(token)
		if strings.ContainsAny(token, ":_/.-") {
			for part := range strings.FieldsFuncSeq(token, func(r rune) bool {
				switch r {
				case ':', '_', '/', '.', '-':
					return true
				default:
					return false
				}
			}) {
				add(part)
			}
		}
	}
	s.out = out
	return out
}

func addWeightedTerms(dst map[string]float64, text string, weight float64, scratch *termScratch) {
	for _, term := range scratch.terms(text) {
		dst[term] += weight
	}
}

func acceptableTerm(term string) bool {
	if isNumeric(term) {
		return false
	}
	if stopwords[term] {
		return false
	}
	if len(term) >= 3 {
		return true
	}
	switch term {
	case "go", "c++", "sql", "api", "db", "io", "os", "ui", "raii", "cpp", "rust":
		return true
	default:
		return false
	}
}

// candidates builds the working clusters, splits the ones whose own graph has several cores, and
// then measures every remaining cluster. It never renders a page.
func (g *claimGraph) candidates() []clusterCandidate {
	settings := g.settings
	working := g.mergeClusters(settings)
	var groups [][]int
	for _, members := range working {
		groups = append(groups, g.splitMembers(members, settings, 0)...)
	}
	groups = g.splitOversized(groups, settings)
	// The removed naming/review stage was also the quality gate for a candidate that had grown
	// across many subjects. Without it, a group that spans dozens of topics must be split
	// deterministically: a page-sized group was allowed to be broad, a CLUSTER is not.
	groups = g.splitBroad(groups, settings)
	groups = g.mergeLeftovers(groups, settings)
	// Stable order: the first claim of a group is its identity in every prompt and test.
	sort.SliceStable(groups, func(i, j int) bool {
		return g.firstMember(groups[i]) < g.firstMember(groups[j])
	})
	candidates := make([]clusterCandidate, 0, len(groups))
	for _, members := range groups {
		metrics := g.metricsOf(members, settings)
		candidates = append(candidates, clusterCandidate{
			Members: members,
			Metrics: metrics,
		})
	}
	g.measureExternal(candidates, settings)
	return candidates
}

// mergeLeftovers groups isolated one-claim fragments by their flat topic. Those claims have no
// content edge strong enough for the graph, so the alternative is one page per claim — the topic is
// the only signal left. The group is capped and split like any other oversized candidate, and it
// never merges into a real multi-claim cluster.
func (g *claimGraph) mergeLeftovers(groups [][]int, settings ClusterSettings) [][]int {
	byTopic := map[string][]int{}
	var out [][]int
	for _, group := range groups {
		if len(group) != 1 {
			out = append(out, group)
			continue
		}
		topic := strings.ToLower(strings.TrimSpace(g.atoms[group[0]].Topic))
		if topic == "" {
			out = append(out, group)
			continue
		}
		byTopic[topic] = append(byTopic[topic], group[0])
	}
	topicNames := make([]string, 0, len(byTopic))
	for topic := range byTopic {
		topicNames = append(topicNames, topic)
	}
	sort.Strings(topicNames)
	leftoverCap := max(settings.MaxClusterClaims/4, 8)
	for _, topic := range topicNames {
		members := byTopic[topic]
		sort.Ints(members)
		if len(members) == 1 {
			out = append(out, members)
			continue
		}
		if len(members) <= leftoverCap {
			out = append(out, members)
			continue
		}
		// A large pile of isolated claims in one topic is still a topic, not a subject. Cut it by
		// content; the model reviews it because it is over the high-risk threshold.
		out = append(out, g.splitOversizedPart(members, ClusterSettings{
			MaxClusterClaims: leftoverCap,
			MinClaimScore:    settings.MinClaimScore,
			SplitMinCoreSize: settings.SplitMinCoreSize,
			MergeMinInternal: settings.MergeMinInternal,
			MergeMinGap:      settings.MergeMinGap,
		})...)
	}
	return out
}

// splitOversized is the safety net against a book-sized candidate. It is not a target page size:
// a normal cluster is never cut. But a candidate above MaxClusterClaims is a failed merge by
// definition, and the seed-and-grow splitter cuts it into coherent pieces before the model sees it.
func (g *claimGraph) primaryNode(member int) string {
	keys := g.nodeKeys[member]
	if len(keys) == 0 {
		return ""
	}
	// The keys are kept sorted, so the first one IS the deterministic name of the claim's node.
	return keys[0]
}

func (g *claimGraph) splitOversized(groups [][]int, settings ClusterSettings) [][]int {
	var out [][]int
	for _, group := range groups {
		if len(group) <= settings.MaxClusterClaims {
			out = append(out, group)
			continue
		}
		// A shared source node is the strongest split signal a large candidate has: the claims
		// were distilled from the same outline section. Cut by node first, then by content only
		// inside a node that is still too large.
		byNode := map[string][]int{}
		for _, member := range group {
			key := g.primaryNode(member)
			if key == "" {
				key = "~none"
			}
			byNode[key] = append(byNode[key], member)
		}
		if len(byNode) > 1 {
			keys := make([]string, 0, len(byNode))
			for key := range byNode {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				part := byNode[key]
				sort.Ints(part)
				out = append(out, g.splitOversizedPart(part, settings)...)
			}
			continue
		}
		out = append(out, g.splitOversizedPart(group, settings)...)
	}
	return out
}

// splitBroad cuts a candidate that spans more than TopicSpanLimit topics into one group per topic.
// The graph's content edges are the right signal for "these claims belong together"; the topic is
// the right signal for "this group has become a book of unrelated subjects". A group that is still
// broad after the core split is therefore cut by its flat topics, deterministically. Small topic
// groups are left for mergeLeftovers, which never merges across topics.
func (g *claimGraph) splitBroad(groups [][]int, settings ClusterSettings) [][]int {
	limit := settings.TopicSpanLimit
	if limit <= 0 {
		limit = 12
	}
	var out [][]int
	for _, group := range groups {
		topics := map[string]bool{}
		for _, member := range group {
			if t := strings.ToLower(strings.TrimSpace(g.atoms[member].Topic)); t != "" {
				topics[t] = true
			}
		}
		if len(topics) <= limit {
			out = append(out, group)
			continue
		}
		byTopic := map[string][]int{}
		for _, member := range group {
			t := strings.ToLower(strings.TrimSpace(g.atoms[member].Topic))
			if t == "" {
				t = "unsorted"
			}
			byTopic[t] = append(byTopic[t], member)
		}
		names := make([]string, 0, len(byTopic))
		for name := range byTopic {
			names = append(names, name)
		}
		sort.SliceStable(names, func(i, j int) bool {
			if len(byTopic[names[i]]) != len(byTopic[names[j]]) {
				return len(byTopic[names[i]]) > len(byTopic[names[j]])
			}
			return g.firstMember(byTopic[names[i]]) < g.firstMember(byTopic[names[j]])
		})
		maxMembers := settings.MaxClusterClaims
		for _, name := range names {
			sub := byTopic[name]
			// A topic group is already one subject; only the size cap can still need a split.
			if maxMembers <= 0 || len(sub) <= maxMembers {
				out = append(out, sub)
				continue
			}
			out = append(out, g.splitOversizedPart(sub, settings)...)
		}
	}
	return out
}

func (g *claimGraph) splitOversizedPart(group []int, settings ClusterSettings) [][]int {
	if len(group) <= settings.MaxClusterClaims {
		return g.splitMembers(group, settings, 0)
	}
	atoms := make([]entities.Atom, 0, len(group))
	byID := make(map[string]int, len(group))
	for _, member := range group {
		atoms = append(atoms, g.atoms[member])
		byID[g.atoms[member].ID.String()] = member
	}
	df := map[string]int{}
	for _, a := range atoms {
		for term := range termSet(a) {
			df[term]++
		}
	}
	common := func(term string) bool {
		floor := max(len(atoms)/2, 3)
		return df[term] >= floor
	}
	var out [][]int
	for _, chunk := range splitBySimilarity(atoms, settings.MaxClusterClaims, common) {
		idxs := make([]int, 0, len(chunk))
		for _, a := range chunk {
			if member, ok := byID[a.ID.String()]; ok {
				idxs = append(idxs, member)
			}
		}
		if len(idxs) == 0 {
			continue
		}
		sort.Ints(idxs)
		out = append(out, g.splitMembers(idxs, settings, 0)...)
	}
	return out
}

func (g *claimGraph) firstMember(members []int) int {
	if len(members) == 0 {
		return math.MaxInt
	}
	first := members[0]
	for _, m := range members[1:] {
		if m < first {
			first = m
		}
	}
	return first
}

// mergeClusters is a greedy content merge: strongest claim edge first, but only while the merged
// group still keeps its internal coherence. It deliberately produces too many small clusters rather
// than one broad page.
func (g *claimGraph) mergeClusters(settings ClusterSettings) [][]int {
	n := len(g.atoms)
	parent := make([]int, n)
	members := make([][]int, n)
	sums := make([]sparseVec, n)
	normSq := make([]float64, n)
	pairSum := make([]float64, n)
	topics := make([]map[string]int, n)
	for i := range parent {
		parent[i] = i
		members[i] = []int{i}
		sums[i] = g.corpus.normalizedVec(i)
		normSq[i] = sums[i].normSq()
		pairSum[i] = normSq[i]
		topics[i] = map[string]int{}
		if topic := strings.TrimSpace(g.atoms[i].Topic); topic != "" {
			topics[i][topic] = 1
		}
	}
	var find func(int) int = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	mergeRoots := func(ra, rb int, force bool) bool {
		if ra == rb {
			return false
		}
		if len(members[ra]) < len(members[rb]) {
			ra, rb = rb, ra
		}
		cross := dot(sums[ra], sums[rb])
		newNormSq := normSq[ra] + normSq[rb] + 2*cross
		newPairSum := pairSum[ra] + pairSum[rb] + 2*cross
		newCount := len(members[ra]) + len(members[rb])
		newInternal := 0.0
		if newNormSq > 0 {
			newInternal = newPairSum / (float64(newCount) * math.Sqrt(newNormSq))
		}
		span := len(topics[ra])
		for topic := range topics[rb] {
			if _, ok := topics[ra][topic]; !ok {
				span++
			}
		}
		if !force && !canMerge(newInternal, newCount, span, settings) {
			return false
		}
		parent[rb] = ra
		members[ra] = append(members[ra], members[rb]...)
		sums[ra].mergeInto(sums[rb])
		for topic, count := range topics[rb] {
			topics[ra][topic] += count
		}
		// rb is not a root any more, so its bookkeeping is dead: a merge that leaves the absorbed
		// group's map behind is a second copy of the graph.
		sums[rb] = sparseVec{}
		topics[rb] = nil
		members[rb] = nil
		normSq[ra] = newNormSq
		pairSum[ra] = newPairSum
		return true
	}

	// The source OUTLINE NODE is the strongest seed a claim has: all claims distilled from one
	// section are one part of a document. Seed those units first, then let content edges merge
	// them across nodes. Without this seed a short claim with few terms becomes a one-claim page.
	byNode := map[string][]int{}
	for i := range g.atoms {
		if key := g.primaryNode(i); key != "" {
			byNode[key] = append(byNode[key], i)
		}
	}
	nodeKeys := make([]string, 0, len(byNode))
	for key := range byNode {
		nodeKeys = append(nodeKeys, key)
	}
	sort.Strings(nodeKeys)
	for _, key := range nodeKeys {
		group := byNode[key]
		if len(group) < 2 || len(group) > settings.MaxClusterClaims {
			continue
		}
		ra := find(group[0])
		for _, member := range group[1:] {
			if rb := find(member); rb != ra {
				mergeRoots(ra, rb, true)
				ra = find(ra)
			}
		}
	}

	for _, edge := range g.edges {
		ra, rb := find(edge.a), find(edge.b)
		if ra == rb {
			continue
		}
		if !g.sameBlock(edge.a, edge.b) {
			// Topic/node is a BLOCKING signal, not a partition: it keeps the first pass from gluing
			// different sections together through a few generic words. Small fragments are joined
			// after the block merge, once, in mergeAcrossTopics — not edge by edge, which would let
			// one block absorb another claim at a time.
			continue
		}
		mergeRoots(ra, rb, false)
	}
	out := make([][]int, 0, n)
	for i := range parent {
		if find(i) != i {
			continue
		}
		group := append([]int(nil), members[i]...)
		sort.Ints(group)
		out = append(out, group)
	}
	sort.SliceStable(out, func(i, j int) bool { return g.firstMember(out[i]) < g.firstMember(out[j]) })
	return g.mergeAcrossTopics(out, settings)
}

// mergeAcrossTopics is the narrow door left open in the topic blocking. Two topic blocks merge only
// when several claims actually connect them, and the merged group must still be coherent. This is
// how a subject that was split across two topic labels becomes one page; a shared "c++" is not
// enough.
func (g *claimGraph) mergeAcrossTopics(groups [][]int, settings ClusterSettings) [][]int {
	if len(groups) < 2 {
		return groups
	}
	memberGroup := map[int]int{}
	for i, group := range groups {
		for _, member := range group {
			memberGroup[member] = i
		}
	}
	type pair struct {
		a, b  int
		count int
		score float64
	}
	edges := map[[2]int]*pair{}
	for _, edge := range g.edges {
		a, okA := memberGroup[edge.a]
		b, okB := memberGroup[edge.b]
		if !okA || !okB || a == b {
			continue
		}
		if g.sameBlock(edge.a, edge.b) {
			continue
		}
		key := [2]int{a, b}
		if a > b {
			key = [2]int{b, a}
		}
		p := edges[key]
		if p == nil {
			p = &pair{a: key[0], b: key[1]}
			edges[key] = p
		}
		p.count++
		p.score += edge.score
	}
	list := make([]pair, 0, len(edges))
	for _, p := range edges {
		list = append(list, *p)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		if list[i].a != list[j].a {
			return list[i].a < list[j].a
		}
		return list[i].b < list[j].b
	})
	parent := make([]int, len(groups))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, p := range list {
		if p.count < settings.CrossTopicMinEdges {
			continue
		}
		ra, rb := find(p.a), find(p.b)
		if ra == rb {
			continue
		}
		// Cross-topic merging is for SMALL fragments. Two substantial topic blocks stay apart
		// unless a model review merges them; automatic merging of large blocks is exactly how a
		// page becomes a book.
		if len(groups[ra]) > 1 && len(groups[rb]) > 1 {
			continue
		}
		merged := append(append([]int(nil), groups[ra]...), groups[rb]...)
		if g.internalOf(merged) < settings.MergeMinInternal+0.08 {
			continue
		}
		parent[rb] = ra
		groups[ra] = merged
	}
	out := make([][]int, 0, len(groups))
	for i := range groups {
		if find(i) != i {
			continue
		}
		sort.Ints(groups[i])
		out = append(out, groups[i])
	}
	sort.SliceStable(out, func(i, j int) bool { return g.firstMember(out[i]) < g.firstMember(out[j]) })
	return out
}

func (g *claimGraph) sameBlock(a, b int) bool {
	if a < len(g.nodeKeys) && b < len(g.nodeKeys) {
		ka, kb := g.nodeKeys[a], g.nodeKeys[b]
		if len(ka) > 0 && len(kb) > 0 {
			// Both key lists are sorted, so sharing a node is a merge over two arrays.
			return sharesKey(ka, kb)
		}
	}
	return g.sameTopicBlock(g.atoms[a].Topic, g.atoms[b].Topic)
}

// sharesKey reports whether two sorted key lists have an entry in common.
func sharesKey(a, b []string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			return true
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return false
}

func (g *claimGraph) sameTopicBlock(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" || a == b {
		return true
	}
	// A one-claim topic is a name, not a boundary: blocking it would produce one page per claim.
	// A topic with several claims is a real signal and keeps the first pass from gluing two
	// subjects together; the narrow cross-topic door still exists for genuine connections.
	return g.topicSizes[a] < 3 || g.topicSizes[b] < 3
}

func canMerge(internal float64, claims, topicSpan int, settings ClusterSettings) bool {
	if claims <= 2 {
		return internal >= settings.MinClaimScore
	}
	if internal < settings.MergeMinInternal {
		return false
	}
	// A broad group is allowed only when its claims are unusually coherent. These are alarms, not
	// page-size targets: a real subject can be large, a book of techniques cannot.
	if claims > settings.MaxClusterClaims && internal < settings.MergeMinInternal+0.06 {
		return false
	}
	if topicSpan > settings.TopicSpanLimit && internal < settings.MergeMinInternal+0.04 {
		return false
	}
	return true
}

// splitThreshold is relative to the cluster's OWN edge strength. An overlap score and a centroid
// cosine live on different scales; comparing them directly made every real cluster look like four
// singletons. Weak bridges inside an otherwise coherent group still fall below this line.
func (g *claimGraph) splitThreshold(members []int, settings ClusterSettings) float64 {
	inside := make(map[int]bool, len(members))
	for _, member := range members {
		inside[member] = true
	}
	total, count := 0.0, 0
	for _, member := range members {
		at, score := g.neighbors[member].at, g.neighbors[member].score
		for k, neighbor := range at {
			if !inside[int(neighbor)] || int(neighbor) < member {
				continue
			}
			total += score[k]
			count++
		}
	}
	floor := settings.MinClaimScore * 0.75
	if count == 0 {
		return floor
	}
	return math.Max(floor, (total/float64(count))*0.70)
}

// splitMembers divides a group at its weakest internal bridges. It only splits when at least two
// cores of a useful size exist; a split that would leave a pile of one-claim fragments is not a
// split, it is a failed merge.
func (g *claimGraph) splitMembers(members []int, settings ClusterSettings, depth int) [][]int {
	if len(members) < 2*settings.SplitMinCoreSize || depth > 4 {
		return [][]int{members}
	}
	threshold := g.splitThreshold(members, settings)
	parts := g.components(members, threshold)
	if len(parts) < 2 {
		return [][]int{members}
	}
	sort.SliceStable(parts, func(i, j int) bool {
		if len(parts[i]) != len(parts[j]) {
			return len(parts[i]) > len(parts[j])
		}
		return g.firstMember(parts[i]) < g.firstMember(parts[j])
	})
	large := 0
	for _, part := range parts {
		if len(part) >= settings.SplitMinCoreSize {
			large++
		}
	}
	if large < 2 {
		return [][]int{members}
	}
	var out [][]int
	for _, part := range parts {
		if len(part) >= settings.SplitMinCoreSize && !g.cohesiveMembers(part, settings) {
			out = append(out, g.splitMembers(part, settings, depth+1)...)
			continue
		}
		out = append(out, part)
	}
	if len(out) < 2 {
		return [][]int{members}
	}
	sort.SliceStable(out, func(i, j int) bool { return g.firstMember(out[i]) < g.firstMember(out[j]) })
	return out
}

func (g *claimGraph) cohesiveMembers(members []int, settings ClusterSettings) bool {
	metrics := g.metricsOf(members, settings)
	return metrics.Claims <= 4 || (metrics.Internal >= settings.MergeMinInternal && metrics.Cores <= 1)
}

func (g *claimGraph) components(members []int, threshold float64) [][]int {
	inside := make(map[int]int, len(members))
	for i, m := range members {
		inside[m] = i
	}
	parent := make([]int, len(members))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, member := range members {
		at, score := g.neighbors[member].at, g.neighbors[member].score
		for k, neighbor := range at {
			j, ok := inside[int(neighbor)]
			if !ok || score[k] < threshold {
				continue
			}
			a, b := find(inside[member]), find(j)
			if a != b {
				parent[b] = a
			}
		}
	}
	byRoot := map[int][]int{}
	for i, member := range members {
		root := find(i)
		byRoot[root] = append(byRoot[root], member)
	}
	out := make([][]int, 0, len(byRoot))
	for _, group := range byRoot {
		sort.Ints(group)
		out = append(out, group)
	}
	sort.SliceStable(out, func(i, j int) bool { return g.firstMember(out[i]) < g.firstMember(out[j]) })
	return out
}

func (g *claimGraph) internalOf(members []int) float64 {
	if len(members) == 0 {
		return 0
	}
	sum := sparseVec{}
	for _, member := range members {
		sum.mergeInto(g.corpus.normalizedVec(member))
	}
	norm := math.Sqrt(sum.normSq())
	if norm == 0 {
		return 0
	}
	total := 0.0
	for _, member := range members {
		total += g.corpus.dotNormalizedVec(member, sum) / norm
	}
	return total / float64(len(members))
}

func (g *claimGraph) metricsOf(members []int, settings ClusterSettings) clusterMetrics {
	m := clusterMetrics{Claims: len(members), Internal: g.internalOf(members)}
	topicCounts := map[string]int{}
	for _, member := range members {
		if topic := strings.TrimSpace(g.atoms[member].Topic); topic != "" {
			topicCounts[topic]++
		}
	}
	m.Topics = len(topicCounts)
	m.TopicEntropy = entropy(topicCounts, len(members))
	m.Cores = len(g.components(members, g.splitThreshold(members, settings)))
	m.CoreTerms = g.coreTerms(members)
	return m
}

func (g *claimGraph) measureExternal(candidates []clusterCandidate, settings ClusterSettings) {
	centroids := make([]sparseVec, len(candidates))
	head := make([][]termWeight, len(candidates))
	l1 := make([]float64, len(candidates))
	postings := map[int32][]int{}
	for i, candidate := range candidates {
		sum := sparseVec{}
		for _, member := range candidate.Members {
			sum.mergeInto(g.corpus.rawVec(member))
		}
		centroids[i] = sum
		l1[i] = sum.l1()
		terms := sum.top(g.corpus.terms, 24)
		head[i] = terms
		for _, tw := range terms {
			postings[tw.term] = append(postings[tw.term], i)
		}
	}
	for i := range candidates {
		scores := map[int]float64{}
		for _, member := range head[i] {
			if len(postings[member.term]) > len(candidates)/2 && len(candidates) > 8 {
				continue
			}
			for _, j := range postings[member.term] {
				if i == j {
					continue
				}
				other := centroids[j].get(member.term)
				if other == 0 { // a raw weight is always positive: zero means "not carried"
					continue
				}
				scores[j] += math.Min(member.weight, other)
			}
		}
		best := 0.0
		for j, shared := range scores {
			denom := math.Min(l1[i], l1[j])
			if denom <= 0 {
				continue
			}
			sim := shared / denom
			if sim > best {
				best = sim
			}
		}
		candidates[i].Metrics.External = best
		candidates[i].Metrics.Gap = candidates[i].Metrics.Internal - best
	}
}

func (g *claimGraph) coreTerms(members []int) []string {
	weights := sparseVec{}
	for _, member := range members {
		weights.mergeInto(g.corpus.normalizedVec(member))
	}
	out := make([]string, 0, 8)
	for _, term := range weights.top(g.corpus.terms, 8) {
		out = append(out, g.corpus.name(term.term))
	}
	return out
}

func entropy(counts map[string]int, total int) float64 {
	if total == 0 || len(counts) < 2 {
		return 0
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.Sort(names)
	value := 0.0
	for _, name := range names {
		p := float64(counts[name]) / float64(total)
		value -= p * math.Log(p)
	}
	return value
}
