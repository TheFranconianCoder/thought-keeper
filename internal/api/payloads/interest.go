package payloads

import (
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// Interest is one standing rule as the manager shows it.
type Interest struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Terms    []string `json:"terms"`
	Exclude  []string `json:"exclude,omitempty"`
	Kinds    []string `json:"kinds,omitempty"`
	Enabled  bool     `json:"enabled"`
	Open     int      `json:"open"`
	Rejected []string `json:"rejected,omitempty"`
	Created  string   `json:"created,omitempty"`
	Updated  string   `json:"updated,omitempty"`
}

// InterestList is GET /api/v1/interests.
type InterestList struct {
	Interests []Interest `json:"interests"`
	Count     int        `json:"count"`
}

// InterestRequest is the body of POST/PUT /interests and POST /interests/preview. Enabled is a
// pointer so "not sent" can mean "on" for a new rule without making "off" impossible.
type InterestRequest struct {
	Name          string   `json:"name"`
	Terms         []string `json:"terms"`
	Exclude       []string `json:"exclude"`
	Kinds         []string `json:"kinds"`
	Enabled       *bool    `json:"enabled"`
	QueueExisting bool     `json:"queue_existing"`
}

// InterestTerm is one term's evidence in one source.
type InterestTerm struct {
	Term   string   `json:"term"`
	Topics []string `json:"topics,omitempty"`
	Claims int      `json:"claims,omitempty"`
	Title  bool     `json:"title,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

// InterestMatch is one source's result for one rule: the band and the evidence. It is used by the
// preview and by the source page's rail, so the two can never disagree about why a source matched.
type InterestMatch struct {
	Slug     string         `json:"slug,omitempty"`
	Name     string         `json:"name"`
	Band     string         `json:"band"`
	Terms    []InterestTerm `json:"terms,omitempty"`
	Topics   []string       `json:"topics,omitempty"`
	Hash     string         `json:"hash,omitempty"`
	Title    string         `json:"title,omitempty"`
	Kind     string         `json:"kind,omitempty"`
	Queued   bool           `json:"queued,omitempty"`
	Rejected bool           `json:"rejected,omitempty"`
	Enabled  bool           `json:"enabled"`
}

// InterestPreview is the editor's calibration result.
type InterestPreview struct {
	Total    int             `json:"total"`
	Strong   int             `json:"strong"`
	Weak     int             `json:"weak"`
	Examples []InterestMatch `json:"examples"`
}

// SourceInterests is GET /api/v1/sources/{hash}/interests.
type SourceInterests struct {
	Interests []InterestMatch `json:"interests"`
}

// InterestReject is the body of PUT /interests/{slug}/sources/{hash}. accepted=true clears the
// correction ("yes, this fits"); accepted=false records it ("not this interest").
type InterestReject struct {
	Accepted bool `json:"accepted"`
}

// InterestListFromUseCase maps the rules to the wire.
func InterestListFromUseCase(in []usecases.InterestView) InterestList {
	out := InterestList{Interests: make([]Interest, 0, len(in))}
	for _, view := range in {
		out.Interests = append(out.Interests, InterestFromEntity(view.Interest, view.Open))
	}
	out.Count = len(out.Interests)
	return out
}

// InterestFromEntity maps one rule.
func InterestFromEntity(rule entities.Interest, open int) Interest {
	return Interest{
		Slug: rule.Slug, Name: rule.Name, Terms: rule.Terms, Exclude: rule.Exclude,
		Kinds: rule.Kinds, Enabled: rule.Enabled, Open: open, Rejected: rule.Rejected,
		Created: interestTime(rule.Created), Updated: interestTime(rule.Updated),
	}
}

// InterestMatchesFromUseCase maps the match list (rail or preview).
func InterestMatchesFromUseCase(in []usecases.IntersectionMatch) []InterestMatch {
	out := make([]InterestMatch, 0, len(in))
	for _, m := range in {
		terms := make([]InterestTerm, 0, len(m.Terms))
		for _, t := range m.Terms {
			terms = append(terms, InterestTerm{
				Term: t.Term, Topics: t.Topics, Claims: t.Claims, Title: t.Title, Tags: t.Tags,
			})
		}
		out = append(out, InterestMatch{
			Slug: m.Slug, Name: m.Name, Band: m.Band, Terms: terms, Topics: m.Topics,
			Hash: m.Hash, Title: m.Title, Kind: string(m.Kind), Queued: m.Queued,
			Rejected: m.Rejected, Enabled: m.Enabled,
		})
	}
	return out
}

// InterestPreviewFromUseCase maps the calibration result.
func InterestPreviewFromUseCase(in usecases.InterestPreview) InterestPreview {
	return InterestPreview{
		Total: in.Total, Strong: in.Strong, Weak: in.Weak,
		Examples: InterestMatchesFromUseCase(in.Examples),
	}
}

func interestTime(t time.Time) string { return wireTime(t) }
