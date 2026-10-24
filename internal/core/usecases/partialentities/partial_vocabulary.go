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
	// New are the observed names the vocabulary has no opinion about yet.
	New []string
	// Top is the biggest names, for the reader and for a curation prompt.
	Top []NameUses
	// Observed is EVERY name that exists right now (the whole catalog, not the head). A reconciliation
	// needs the whole set to write the baseline; the wire payload leaves it out, because the reader
	// wants the numbers and the head, not five thousand names.
	Observed []NameUses
}
