package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// RunOp is the class of a run.
type RunOp string

const (
	// RunPipeline is a run of the PIPELINE: the four steps capture → outline → distill → cluster
	// all run under this class, and the run's Step says which of them it executed.
	RunPipeline RunOp = "pipeline"
	RunQuery    RunOp = "query"
	RunLint     RunOp = "lint"

	// RunTag derives a wiki PAGE's tags from its own title and prose. It is a small, bounded call, and
	// it has its own queue so a page save never waits for it and a slow model never blocks a document.
	RunTag RunOp = "tag"
	// RunSummary writes one source's SUMMARY sidecar from its claims. It is a single bounded call,
	// scheduled after a complete distill (and by the consistency job for a missing file), with its
	// own queue so a slow model never blocks a document.
	RunSummary RunOp = "summary"
	// RunVocabulary is a curation of the claim-topic vocabulary: it asks the model what the names
	// should become and applies the answer. It rewrites atoms, so it takes the exclusive gate like the
	// cluster step.
	RunVocabulary RunOp = "vocabulary"

	// RunCluster is step 4 of the pipeline: the cluster pass. Like the two above it is a routing
	// op that persists as a pipeline run (the runs table's op class stays stable); the run's `step`
	// column is what says it was the cluster step.
	RunCluster RunOp = "cluster"
)

// RunStatus is the lifecycle of a run.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunAborted   RunStatus = "aborted"
)

// Run records one agent operation.
type Run struct {
	ID ulid.ULID
	Op RunOp
	// Step names the pipeline step this run executes (capture, outline, distill, cluster). The
	// op stays the class of work; the step is what the four lights and the pipeline status read.
	Step             Step
	Model            string
	TargetID         string // source hash (pipeline) or note id (query/lint)
	Status           RunStatus
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Iterations       int
	// Question is what a QUERY run was asked (empty for every other run).
	Question  string
	Answer    string
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
}

// RunStep records one tool call of a run.
type RunStep struct {
	RunID        ulid.ULID
	Seq          int
	Tool         string
	ArgsDigest   string
	ResultDigest string
	IsError      bool
	DurationMS   int
}
