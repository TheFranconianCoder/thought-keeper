package payloads

// AtomSource is provenance from an atom to a source node.
type AtomSource struct {
	Hash  string `json:"hash"`
	Title string `json:"title,omitempty"`
	Node  string `json:"node,omitempty"`
	OCR   bool   `json:"ocr,omitempty"`
}

// Atom is the wire shape of an atom.
type Atom struct {
	ID          string       `json:"id"`
	Topic       string       `json:"topic"`
	Path        string       `json:"path"`
	Claim       string       `json:"claim"`
	Body        string       `json:"body"`
	Type        string       `json:"type"`
	Status      string       `json:"status"`
	Sources     []AtomSource `json:"sources,omitempty"`
	SourceCount int          `json:"source_count"`
	// Clusters are the associations this claim belongs to. They are IDS, not navigation: a cluster
	// has no title and is never listed, so the Claims detail view uses them to show the claim's
	// PEERS (the claims it travels with), which is the only way the association is visible.
	Clusters     []string `json:"clusters,omitempty"`
	ClusterCount int      `json:"cluster_count"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// AtomRelated is the response of GET /api/v1/atoms/{id}/related. The two relations are separate
// lists because they are separate answers: a cluster peer travels with the claim, a same-topic claim
// merely shares its subject.
type AtomRelated struct {
	Peers     []Atom `json:"peers"`
	SameTopic []Atom `json:"same_topic"`
	Count     int    `json:"count"`
}

// AtomList is the response of GET /api/v1/atoms and of a source's claims. For a source the list is
// BOUNDED (`limit`): Total is how many claims it really has, and Topics describe all of them, so the
// overlay can show the picture without shipping every body.
type AtomList struct {
	Atoms  []Atom        `json:"atoms"`
	Count  int           `json:"count"`
	Total  int           `json:"total,omitempty"`
	Topics []SourceTopic `json:"topics,omitempty"`
	// Command is set when the list is an assistant result (see SourceList.Command).
	Command *CommandRef `json:"command,omitempty"`
}

// SourceTopic is one of a source's claim topics and how many of its claims carry it: the rail shows
// the first few by count, which is what the source is actually about.
type SourceTopic struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// AtomLayout is the response of GET /api/v1/atoms/layout (what a reorganize WOULD move) and of
// POST /api/v1/atoms/reorganize (what it DID).
//
// The app writes claims at `atoms/<ab>/<cd>/<id>.md` — addressed by their ID, as a source is
// addressed by its hash. A vault written before that keeps them in branch folders; this is the
// account of moving it.
type AtomLayout struct {
	// Folders are the branch folders found, Claims how many claims they carry and Indexes how many
	// generated branch index files stand beside them.
	Folders []string `json:"folders,omitempty"`
	Claims  int      `json:"claims"`
	Indexes int      `json:"indexes,omitempty"`
	// Moved/Removed/IndexesRemoved/Skipped are the reorganize's own numbers; Kept names the folders
	// that still hold something afterwards (an unreadable file is never thrown away).
	Moved          int      `json:"moved"`
	Removed        int      `json:"folders_removed"`
	IndexesRemoved int      `json:"indexes_removed,omitempty"`
	Skipped        int      `json:"skipped,omitempty"`
	Kept           []string `json:"kept,omitempty"`
}

// ClaimCode is the response of POST /api/v1/atoms/quote-code: the one-time repair that wraps the
// bracket syntax a claim body quotes from its source in inline code.
type ClaimCode struct {
	Scanned   int `json:"scanned"`
	Repaired  int `json:"repaired"`
	Fragments int `json:"fragments"`
	Skipped   int `json:"skipped"`
}

// AtomDuplicates is the response of GET /api/v1/atoms/duplicates (what a collapse WOULD take out) and
// of POST /api/v1/atoms/collapse (what it DID).
//
// A duplicate is the same claim written more than once — same branch, statement, provenance, body,
// type, status and tags; only the ID differs. They exist because a write that could not be recorded
// left its file behind, and a model that retries a write it cannot see leaves one file per attempt.
type AtomDuplicates struct {
	Groups    int    `json:"groups"`
	Files     int    `json:"files"`
	Collapsed int    `json:"collapsed"`
	Example   string `json:"example,omitempty"`
}

// TopicUse is a claim topic with its atom count.
type TopicUse struct {
	Topic string `json:"topic"`
	Count int    `json:"count"`
}

// TopicUseList is the response of GET /api/v1/topics/claims.
type TopicUseList struct {
	Topics []TopicUse `json:"topics"`
	Count  int        `json:"count"`
}

// DistillRequest is the body of POST /api/v1/sources/{hash}/distill.
type DistillRequest struct {
	// Reset throws the source's previous derivation away (capture, outline, claims) and builds it
	// again instead of continuing it. The cluster records that cited them are pruned by the next
	// cluster pass.
	Reset bool `json:"reset"`
}

// DistillResponse is the response of POST /api/v1/sources/{hash}/distill.
type DistillResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// Run is the wire shape of an agent run.
type Run struct {
	ID               string `json:"id"`
	Op               string `json:"op"`
	Model            string `json:"model"`
	TargetID         string `json:"target_id,omitempty"`
	Status           string `json:"status"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Iterations       int    `json:"iterations"`
	// Question is what a QUERY run was asked.
	Question  string    `json:"question,omitempty"`
	Answer    string    `json:"answer,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt string    `json:"started_at"`
	EndedAt   string    `json:"ended_at,omitempty"`
	Steps     []RunStep `json:"steps,omitempty"`
}

// RunStep is one tool call of a run.
type RunStep struct {
	Seq        int    `json:"seq"`
	Tool       string `json:"tool"`
	IsError    bool   `json:"is_error,omitempty"`
	DurationMS int    `json:"duration_ms"`
}
