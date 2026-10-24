package payloads

import "github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"

// HitsFromPartials maps lexical search hits to the wire.
func HitsFromPartials(ps []partialentities.PartialHit) []SearchHit {
	out := make([]SearchHit, 0, len(ps))
	for _, p := range ps {
		out = append(out, SearchHit{ID: p.ID, Kind: p.Kind, Title: p.Title, Snippet: p.Snippet})
	}
	return out
}

// FindingsFromPartials maps lint findings to the wire.
func FindingsFromPartials(ps []partialentities.PartialFinding) []Finding {
	out := make([]Finding, 0, len(ps))
	for _, p := range ps {
		out = append(out, Finding{
			ID: p.ID, RunID: p.RunID, Layer: p.Layer, Kind: p.Kind,
			Severity: p.Severity, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID, Message: p.Message,
		})
	}
	return out
}
