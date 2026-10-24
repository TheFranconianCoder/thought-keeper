package partialentities

// PartialHit is one lexical search result.
type PartialHit struct {
	ID      string
	Kind    string
	Title   string
	Snippet string
}

// PartialFinding is one lint finding.
type PartialFinding struct {
	ID          string
	RunID       string
	Layer       string
	Kind        string
	Severity    string
	SubjectKind string
	SubjectID   string
	Message     string
}
