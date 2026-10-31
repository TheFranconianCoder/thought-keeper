package payloads

import (
	"fmt"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// SourceFromPartial maps a source partial to its wire shape. This is the single
// place that translates the domain to JSON.
func SourceFromPartial(p partialentities.PartialSource) Source {
	s := Source{
		Hash:            p.Hash,
		Kind:            p.Kind,
		Ext:             p.Ext,
		Path:            p.Path,
		URL:             p.URL,
		Title:           p.Title,
		Language:        p.Language,
		Origin:          p.Origin,
		WikiPage:        p.WikiPage,
		ParentHash:      p.ParentHash,
		Sitemap:         p.Sitemap,
		RSS:             p.RSS,
		FromFeed:        p.FromFeed,
		FeedHash:        p.FeedHash,
		Wall:            p.Wall,
		Attached:        p.Attached,
		AttachedFrom:    p.AttachedFrom,
		OriginalName:    p.OriginalName,
		Tags:            p.Tags,
		AutoTags:        p.AutoTags,
		FavoriteAt:      wireTime(p.FavoriteAt),
		ReadLaterAt:     wireTime(p.ReadLaterAt),
		ReadLaterDoneAt: wireTime(p.ReadLaterDoneAt),
		ReadLaterManual: p.ReadLaterManual,
		ReadLaterVia:    p.ReadLaterVia,
		ReadLater:       !p.ReadLaterAt.IsZero() && p.ReadLaterDoneAt.IsZero(),
		Bytes:           p.Bytes,
		Pages:           p.Pages,
		TreeState:       p.TreeState,
		AtomCount:       p.AtomCount,
		ProvenanceRows:  p.ProvenanceRows,
		Children:        p.Children,
		PartsTotal:      p.PartsTotal,
		PartsDone:       p.PartsDone,
		AtomsLeft:       p.AtomsLeft,
		URLStatus:       p.URLStatus,
		Bookmark:        p.Bookmark,
		ImportedAt:      p.ImportedAt.UTC().Format(time.RFC3339),
	}
	if !p.URLChecked.IsZero() {
		s.URLCheckedAt = p.URLChecked.UTC().Format(time.RFC3339)
	}
	if !p.AutoTagsAt.IsZero() {
		s.AutoTagsAt = p.AutoTagsAt.UTC().Format(time.RFC3339)
	}
	s.Steps = stepsFromPartial(p)
	return s
}

// wireTime formats an optional timestamp: empty means the mark is absent, never "1970".
func wireTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// stepsFromPartial derives the four lights of a source.
//
// The recorded state comes from the source's <hash>.meta file (mirrored into the index) — that is
// the truth for a step that ran. Where nothing was ever recorded (a source from a vault written
// before the steps existed, or a step that derives its state), the light is DERIVED from what the
// index knows, so an older vault shows the same picture instead of four empty circles.
func stepsFromPartial(p partialentities.PartialSource) map[string]StepStatus {
	set := p.Steps
	out := map[string]StepStatus{}
	put := func(step string, st StepStatus) { out[step] = st }

	toWire := func(st entities.StepStatus) StepStatus {
		w := StepStatus{State: string(st.State), Detail: st.Detail, Error: st.Error}
		if !st.At.IsZero() {
			w.At = st.At.UTC().Format(time.RFC3339)
		}
		return w
	}
	recorded := func(step entities.Step) (StepStatus, bool) {
		if set == nil {
			return StepStatus{}, false
		}
		st, ok := set[step]
		if !ok || st.State == "" || st.State == entities.StepPending {
			return StepStatus{}, false
		}
		return toWire(st), true
	}

	// capture: the row exists, so the source was taken in and is readable locally.
	if w, ok := recorded(entities.StepCapture); ok {
		put(string(entities.StepCapture), w)
	} else {
		put(string(entities.StepCapture), StepStatus{State: string(entities.StepOK)})
	}

	if p.Bookmark {
		// A bookmark is collected, not indexed: outline, distill and compile do not apply to it.
		for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill, entities.StepCluster} {
			put(string(step), StepStatus{State: string(entities.StepSkipped), Detail: "bookmark"})
		}
		return out
	}

	// A feed or a sitemap is a LIST of sources: capture fetched the list, and outline, distill and
	// association do not apply to it. Saying so explicitly (rather than leaving a light pending on a
	// step that will never run) is what separates a finished list from a document stuck after step 1.
	if p.RSS || p.Sitemap {
		what := "sitemap"
		if p.RSS {
			what = "feed"
		}
		for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill, entities.StepCluster} {
			put(string(step), StepStatus{State: string(entities.StepSkipped), Detail: what})
		}
		return out
	}

	// outline: what the tree state says.
	if w, ok := recorded(entities.StepOutline); ok {
		put(string(entities.StepOutline), w)
	} else {
		switch p.TreeState {
		case string(entities.TreeBuilt):
			put(string(entities.StepOutline), StepStatus{State: string(entities.StepOK)})
		case string(entities.TreeFailed):
			put(string(entities.StepOutline), StepStatus{State: string(entities.StepFailed)})
		case string(entities.TreePending):
			put(string(entities.StepOutline), StepStatus{State: string(entities.StepRunning)})
		default:
			put(string(entities.StepOutline), StepStatus{State: string(entities.StepPending)})
		}
	}

	// distill: the record if there is one, otherwise "claims exist" (a vault from before the steps).
	//
	// Two rules decide the light, and both are about honesty:
	//   - A step that has NOT BEGUN is an empty circle. An outline whose parts have simply not been
	//     derived yet is not "started, not finished" — nothing has happened to it.
	//   - GREEN MEANS DONE. Done is: every part recorded AND at least one claim produced. A record that
	//     says `ok` over open parts, and a derivation that recorded parts but wrote no claim at all, are
	//     both amber. The second is the state a light must never paint green: parts that produced no
	//     claim mean the derivation yielded nothing, not that it finished.
	// `running` and `failed` are facts about a run and pass through untouched.
	dist := StepStatus{State: string(entities.StepPending)}
	if w, ok := recorded(entities.StepDistill); ok {
		dist = w
	} else if p.AtomCount > 0 {
		// No record: a vault from before the steps were recorded. The same rules still apply.
		dist = StepStatus{State: string(entities.StepOK), Detail: fmt.Sprintf("%d claims", p.AtomCount)}
	}
	if dist.State == string(entities.StepOK) || dist.State == string(entities.StepPending) {
		// "Begun" needs a PRODUCTIVE part or a claim. A source_relative part is metadata: it is
		// correctly done without a claim, but it is not evidence that the document was derived.
		// A document made only of metadata is correctly finished without a claim; it is the one
		// exception to "no claim means not green".
		allMetadata := p.PartsTotal > 0 && p.PartsDone >= p.PartsTotal && p.PartsDerived == 0 && p.AtomCount == 0
		begun := (p.PartsDerived > 0 || p.AtomCount > 0 || allMetadata) && (p.PartsTotal > 0 || p.AtomCount > 0)
		switch {
		case !begun:
			dist = StepStatus{State: string(entities.StepPending)}
		case p.PartsTotal > 0 && p.PartsDone < p.PartsTotal:
			dist = StepStatus{
				State:  string(entities.StepStale),
				Detail: fmt.Sprintf("%d of %d parts recorded — derive again", p.PartsDone, p.PartsTotal),
			}
		case p.PartsDerived == 0 && p.AtomCount == 0:
			// Every text-bearing part carried links or document metadata and correctly produced no
			// claim: a link collection says nothing itself, it points at what does. "source metadata"
			// alone read like book chrome, which is only half of what lands here.
			dist = StepStatus{
				State:  string(entities.StepOK),
				Detail: "no claim — the parts are link lists or metadata",
			}
		case p.AtomCount == 0:
			dist = StepStatus{State: string(entities.StepStale), Detail: "no claim from the parts — derive again"}
		default:
			dist = StepStatus{State: string(entities.StepOK), Detail: fmt.Sprintf("%d claims", p.AtomCount)}
		}
	}
	put(string(entities.StepDistill), dist)

	// The cluster step runs over the whole vault, so per source it is derived: are this source's claims
	// in a cluster yet? Open claims mean the step still owes this source something — so it is STARTED
	// but not finished, which is amber and never green. Only claims that all sit in a cluster are done.
	switch {
	case p.AtomsLeft > 0 && p.AtomCount > p.AtomsLeft:
		// Some claims are already in a cluster and some are not: the cluster step has STARTED and is
		// unfinished, so amber is the honest light.
		put(string(entities.StepCluster), StepStatus{
			State:  string(entities.StepStale),
			Detail: fmt.Sprintf("%d claim(s) without a page", p.AtomsLeft),
		})
	case p.AtomsLeft > 0:
		// Every claim is still unclustered. Nothing of this source has reached the association
		// layer, so the cluster step has NOT STARTED: an empty circle, not the amber "half done"
		// light.
		put(string(entities.StepCluster), StepStatus{State: string(entities.StepPending)})
	case p.AtomCount > 0:
		put(string(entities.StepCluster), StepStatus{State: string(entities.StepOK)})
	case dist.State == string(entities.StepOK):
		// Every document step is through and the source produced no claim (a link list, document
		// metadata, an empty page): there is nothing to associate, so the step is DONE for this source.
		// An empty circle here waited for ever and read as "not clustered" — the association layer
		// cannot group a claim that does not exist.
		put(string(entities.StepCluster), StepStatus{
			State:  string(entities.StepOK),
			Detail: "nothing to associate — the source has no claims",
		})
	default:
		put(string(entities.StepCluster), StepStatus{State: string(entities.StepPending)})
	}
	return out
}

// SourcesFromPartials maps a slice of source partials.
func SourcesFromPartials(ps []partialentities.PartialSource) []Source {
	out := make([]Source, 0, len(ps))
	for _, p := range ps {
		out = append(out, SourceFromPartial(p))
	}
	return out
}

// NodeFromPartial maps a node partial to its wire shape.
func NodeFromPartial(p partialentities.PartialNode) Node {
	return Node{
		NodeID:    p.NodeID,
		ParentID:  p.ParentID,
		Depth:     p.Depth,
		Ord:       p.Ord,
		Title:     p.Title,
		Summary:   p.Summary,
		StartPage: p.StartPage,
		EndPage:   p.EndPage,
		OCR:       p.OCR,
		Text:      p.Text,
		HasText:   p.HasText,
	}
}

// NodesFromPartials maps a slice of node partials.
func NodesFromPartials(ps []partialentities.PartialNode) []Node {
	out := make([]Node, 0, len(ps))
	for _, p := range ps {
		out = append(out, NodeFromPartial(p))
	}
	return out
}
