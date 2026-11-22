package partialentities

// NameUses is one observed vocabulary name and how often it is used.
type NameUses struct {
	Name string
	Uses int
}

// NameGroup is a set of names the numbers say belong together — names that normalize to one key, or
// names that share their words. It is a CANDIDATE: the decision to merge is not made by the numbers.
type NameGroup struct {
	// Reason is "same-key" (they normalize to one name — always a bug) or "shared-words".
	Reason string
	Names  []NameUses
}

// VocabularyHealth is one namespace's numbers: what is observed (the index), what is decided (the
// vault's vocabulary file), and what shape the vocabulary is in.
type VocabularyHealth struct {
	Namespace string
	Names     int
	Uses      int
	// Singletons is the long tail — names used exactly once.
	Singletons     int
	SingletonShare float64
	// Coverage is the share of all uses the biggest names carry: does the head do the work?
	Coverage float64
	// Collisions and NearDuplicates are the free findings — the mechanical half of a curation.
	Collisions     []NameGroup
	NearDuplicates []NameGroup
	// Counts are the whole truth; the lists above may be capped in the payload.
	CollisionCount     int
	NearDuplicateCount int
	// BroadTokens is how many learned broad tokens the detector ignores (language names, generic
	// categories) and OversizedFamilies are components too broad to merge — reported as a count,
	// never as a merge suggestion.
	BroadTokens int
	// BroadTokensLearned is false when the detector is still using the frequency fallback; the
	// curation then learns the set once, even when no new name exists.
	BroadTokensLearned bool
	OversizedFamilies  int
	OversizedNames     int
	// UnreviewedFamilies counts near-duplicate groups that still contain a name the model has not
	// judged. It is what makes the automatic curation ask again after the detector changes — and
	// stops asking once every group has been reviewed.
	UnreviewedFamilies int
	// Canonical/Absorbed is what the vocabulary file already knows.
	Canonical int
	Absorbed  int
	// Shelf is the size of the bounded target vocabulary and Target the size it was built for. A
	// report whose Names run far past the Target is a namespace the coarsening still has to file.
	Shelf  int
	Target int
	// Remaining is what the coarsening would file: the names in use that are neither on the shelf,
	// nor already absorbed, nor already judged. A namespace without a shelf has its whole catalog
	// remaining, which is what it is — the shelf is the target, the rest is the tail.
	Remaining int
	// Retopic are the names whose CLAIMS the retopic pass still has to re-derive: outside the shelf,
	// not absorbed, and not asked about claim by claim yet. Retopiced is how many names that pass has
	// already answered for. RetopicClaims is the same queue in CLAIMS, and it is the number the
	// automatic pass measures its progress by: a pass that spends its budget inside one large name
	// moves claims without settling a name, and a guard that watched the name count stopped a queue
	// that was visibly shrinking.
	Retopic       int
	Retopiced     int
	RetopicClaims int
	// OffShelf is the claim MASS still outside the shelf (the uses of every name that is not on it).
	// It is the honest measure of how far the corpus is from the invariant, which `Remaining` alone
	// cannot say: a hundred names carrying one claim each and a hundred names carrying a hundred
	// claims each are very different work. Retopic, RetopicClaims, Retopiced and OffShelf are
	// TOPICS-only: the tag namespaces report 0, because a tag is not re-derived from a claim.
	OffShelf int
	// CatchAll are the topics whose share of ALL claims is above the catch-all limit, and CatchAllUses
	// the claim mass they carry. Thin are the topics below the thin limit (a singleton is thin in a
	// vault of any size). Both are judged by share alone, so they scale with the corpus. Only the topics
	// namespace is judged: the tag namespaces have no shelf refit.
	CatchAll     int
	CatchAllUses int
	Thin         int
	ThinUses     int
	// New are the observed names the vocabulary has no opinion about yet.
	New []string
	// Top is the biggest names, for the reader and for a curation prompt.
	Top []NameUses
	// Observed is EVERY name that exists right now (the whole catalog, not the head). A reconciliation
	// needs the whole set to write the baseline; the wire payload leaves it out, because the reader
	// wants the numbers and the head, not five thousand names.
	Observed []NameUses
}
