package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// FindingLayer distinguishes deterministic from semantic lint.
type FindingLayer string

const (
	FindingProgrammatic FindingLayer = "programmatic"
	FindingLLM          FindingLayer = "llm"
)

// FindingSeverity sorts findings.
type FindingSeverity string

const (
	SeverityInfo    FindingSeverity = "info"
	SeverityWarning FindingSeverity = "warning"
	SeverityError   FindingSeverity = "error"
)

// SubjectKind is what a finding is about.
type SubjectKind string

const (
	SubjectAtom    SubjectKind = "atom"
	SubjectCluster SubjectKind = "note"
	SubjectSource  SubjectKind = "source"
)

// LintFinding is one health-check result.
type LintFinding struct {
	ID          ulid.ULID
	RunID       ulid.ULID
	Layer       FindingLayer
	Kind        string
	Severity    FindingSeverity
	SubjectKind SubjectKind
	SubjectID   string
	Message     string
	CreatedAt   time.Time
}
