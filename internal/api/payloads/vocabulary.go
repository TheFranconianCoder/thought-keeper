package payloads

import (
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// VocabularyNameUses is one name of a vocabulary and how often it is used.
type VocabularyNameUses struct {
	Name string `json:"name"`
	Uses int    `json:"uses"`
}

// VocabularyGroup is a set of names the numbers say belong together. It is a CANDIDATE: the decision to
// merge is not made here, and a name used once can be the sharpest name there is.
type VocabularyGroup struct {
	Reason string               `json:"reason"`
	Names  []VocabularyNameUses `json:"names"`
}

// VocabularyHealth is one namespace's numbers: observed (the index) against decided (the vault).
type VocabularyHealth struct {
	Namespace string `json:"namespace"`
	Names     int    `json:"names"`
	Uses      int    `json:"uses"`
	// Singletons is the long tail; Coverage is the share of all uses the biggest names carry.
	Singletons     int     `json:"singletons"`
	SingletonShare float64 `json:"singleton_share"`
	Coverage       float64 `json:"coverage"`
	// The mechanical findings: names that normalize to one key (always a bug) and names that share
	// their words (a question worth asking).
	Collisions     []VocabularyGroup `json:"collisions,omitempty"`
	NearDuplicates []VocabularyGroup `json:"near_duplicates,omitempty"`
	// The COUNTS are the whole truth; the lists are capped so the report stays small enough to refresh
	// whenever the vocabulary changes.
	CollisionCount     int `json:"collision_count,omitempty"`
	NearDuplicateCount int `json:"near_duplicate_count,omitempty"`
	// BroadTokens are learned from this vault and ignored by the near-duplicate detector;
	// OversizedFamilies are components too broad to merge (counted, never suggested).
	BroadTokens        int  `json:"broad_tokens,omitempty"`
	BroadTokensLearned bool `json:"broad_tokens_learned,omitempty"`
	OversizedFamilies  int  `json:"oversized_families,omitempty"`
	OversizedNames     int  `json:"oversized_names,omitempty"`
	UnreviewedFamilies int  `json:"unreviewed_families,omitempty"`
	// What the vocabulary file already knows, and the names it has no opinion about yet.
	Canonical int                  `json:"canonical"`
	Absorbed  int                  `json:"absorbed"`
	New       []string             `json:"new,omitempty"`
	Top       []VocabularyNameUses `json:"top,omitempty"`
}

// VocabularyCurateRequest asks for a curation of one namespace.
type VocabularyCurateRequest struct {
	Namespace string `json:"namespace"`
}

// VocabularyReport is the answer of GET /api/v1/vocabulary: every namespace, coarse to fine.
type VocabularyReport struct {
	Namespaces []VocabularyHealth `json:"namespaces"`
}

// VocabularyFromPartials maps the report.
func VocabularyFromPartials(in []partialentities.VocabularyHealth) VocabularyReport {
	out := make([]VocabularyHealth, 0, len(in))
	for _, h := range in {
		out = append(out, VocabularyHealth{
			Namespace: h.Namespace, Names: h.Names, Uses: h.Uses,
			Singletons: h.Singletons, SingletonShare: h.SingletonShare, Coverage: h.Coverage,
			Collisions: groupsFromPartials(capGroups(h.Collisions)), NearDuplicates: groupsFromPartials(capGroups(h.NearDuplicates)),
			CollisionCount: h.CollisionCount, NearDuplicateCount: h.NearDuplicateCount,
			BroadTokens: h.BroadTokens, BroadTokensLearned: h.BroadTokensLearned,
			OversizedFamilies: h.OversizedFamilies, OversizedNames: h.OversizedNames,
			UnreviewedFamilies: h.UnreviewedFamilies,
			Canonical:          h.Canonical, Absorbed: h.Absorbed, New: h.New, Top: namesFromPartials(h.Top),
		})
	}
	return VocabularyReport{Namespaces: out}
}

// maxGroupsInPayload caps the finding lists: a grown vocabulary has hundreds, and the reader acts on
// the count and the first few — the whole list is what made the report 193 KB.
const maxGroupsInPayload = 20

func capGroups(in []partialentities.NameGroup) []partialentities.NameGroup {
	if len(in) > maxGroupsInPayload {
		return in[:maxGroupsInPayload]
	}
	return in
}

func groupsFromPartials(in []partialentities.NameGroup) []VocabularyGroup {
	out := make([]VocabularyGroup, 0, len(in))
	for _, g := range in {
		out = append(out, VocabularyGroup{Reason: g.Reason, Names: namesFromPartials(g.Names)})
	}
	return out
}

func namesFromPartials(in []partialentities.NameUses) []VocabularyNameUses {
	out := make([]VocabularyNameUses, 0, len(in))
	for _, n := range in {
		out = append(out, VocabularyNameUses{Name: n.Name, Uses: n.Uses})
	}
	return out
}
