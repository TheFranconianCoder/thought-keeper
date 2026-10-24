package partialentities

import "time"

// PartialBranch is a branch with its atom count.
type PartialBranch struct {
	Topic string
	Count int
}

// PartialRunStep is one tool call of a run.
type PartialRunStep struct {
	Seq        int
	Tool       string
	IsError    bool
	DurationMS int
}

// PartialRun is the DTO for entities.Run.
type PartialRun struct {
	ID               string
	Op               string
	Model            string
	TargetID         string
	Status           string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Iterations       int
	// Question is what a QUERY run was asked (empty for other runs): the Ask view shows it beside the
	// answer, and filing the answer keeps it.
	Question  string
	Answer    string
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
	Steps     []PartialRunStep
}
