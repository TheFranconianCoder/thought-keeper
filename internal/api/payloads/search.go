package payloads

// SearchHit is one lexical search result.
type SearchHit struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// SearchResult is the response of GET /api/v1/search.
type SearchResult struct {
	Hits  []SearchHit `json:"hits"`
	Count int         `json:"count"`
}

// QueryRequest is the body of POST /api/v1/query.
type QueryRequest struct {
	Question string `json:"question"`
	// Source scopes the answer to ONE source and that source's claims. Empty means the whole vault.
	Source string `json:"source,omitempty"`
}

// LintRequest is the body of POST /api/v1/lint.
type LintRequest struct {
	Scope string `json:"scope"`
}

// Finding is one lint finding on the wire.
type Finding struct {
	ID          string `json:"id"`
	RunID       string `json:"run_id,omitempty"`
	Layer       string `json:"layer"`
	Kind        string `json:"kind"`
	Severity    string `json:"severity"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id,omitempty"`
	Message     string `json:"message"`
}

// FindingList is the response of GET /api/v1/lint/findings.
type FindingList struct {
	Findings []Finding `json:"findings"`
	Count    int       `json:"count"`
}

// CleanupResult is the response of POST /api/v1/cleanup.
type CleanupResult struct {
	ClustersPruned  int `json:"clusters_pruned"`
	ClustersRemoved int `json:"clusters_removed"`
	Findings        int `json:"findings"`
}
