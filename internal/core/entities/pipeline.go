package entities

import "maps"

import "time"

// The pipeline has exactly four steps, and they are STICTLY separated:
//
//	capture → outline → distill → compile
//
// capture, outline and distill are DOCUMENT steps: they run for one source, one after the other,
// and they only ever build on each other. A source never influences another source's atoms — what
// one document says is derived from that document alone.
//
// compile is the CLUSTER step: it is the one place where sources meet (a page draws on the atoms of
// several sources), it is considered over the WHOLE vault rather than per source, and it may only
// run while no document step runs.
//
// The further forward something changes, the more of what follows loses its validity: a new
// capture invalidates the outline, distill and the pages that drew on its atoms; a changed atom
// makes the clusters that cite it dirty.
type Step string

const (
	// StepCapture takes a source in and makes it readable locally: Markdown and text are read,
	// a PDF gets its text layer (or OCR), a URL is fetched. The result is a static local
	// snapshot — readable again and again, never dependent on the live page.
	StepCapture Step = "capture"
	// StepOutline builds the hierarchical outline of that snapshot: how the document is
	// structured, part by part.
	StepOutline Step = "outline"
	// StepDistill turns the outline into atoms: one claim per atom, with its flat topic, type and the
	// source node it came from (its provenance).
	StepDistill Step = "distill"
	// StepCluster groups claims into titleless associations. It is the only step that sees more than
	// one source, and the only one that must not run while a document step runs.
	StepCluster Step = "cluster"
)

// PipelineSteps are the four steps in pipeline order.
var PipelineSteps = [...]Step{StepCapture, StepOutline, StepDistill, StepCluster}

// IsDocument reports whether the step runs per source (capture, outline, distill) as opposed to
// over the whole vault (cluster).
func (s Step) IsDocument() bool { return s != StepCluster }

// Valid reports whether s names one of the four steps.
func (s Step) Valid() bool {
	for _, p := range PipelineSteps {
		if s == p {
			return true
		}
	}
	return false
}

// StepState is one step's state for one source: the traffic light.
type StepState string

const (
	// StepPending is an empty circle: the step has not run yet.
	StepPending StepState = "pending"
	// StepRunning is blue: the step is executing right now.
	StepRunning StepState = "running"
	// StepOK is green: the step is done and its result is the one on disk.
	StepOK StepState = "ok"
	// StepFailed is red: the step failed. It blocks every step after it — a broken capture cannot
	// produce an honest outline, and an honest outline is what distill cites.
	StepFailed StepState = "failed"
	// StepSkipped is grey: the step does not apply to this source (a bookmark is captured, but
	// never outlined or distilled).
	StepSkipped StepState = "skipped"
	// StepStale is amber: the step ran, but something it depends on changed since, so its result
	// must be derived again. compile marks a page this way when one of its atoms changed.
	StepStale StepState = "stale"
)

// StepStatus is the persisted state of ONE step for ONE source. It is what the <hash>.meta file
// holds (for capture, outline and distill) and what the four lights are drawn from.
type StepStatus struct {
	State  StepState `json:"state"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
	Error  string    `json:"error,omitempty"`
}

// StepSet is the four steps' state of one source.
type StepSet map[Step]StepStatus

// NewStepSet returns an empty set in which every step is pending.
func NewStepSet() StepSet { return StepSet{} }

// State returns the step's state, pending when nothing was recorded.
func (s StepSet) State(step Step) StepState {
	if s == nil {
		return StepPending
	}
	if st, ok := s[step]; ok && st.State != "" {
		return st.State
	}
	return StepPending
}

// Get returns the step's recorded status.
func (s StepSet) Get(step Step) StepStatus {
	if s == nil {
		return StepStatus{State: StepPending}
	}
	st, ok := s[step]
	if !ok {
		return StepStatus{State: StepPending}
	}
	if st.State == "" {
		st.State = StepPending
	}
	return st
}

// With returns a copy of the set with one step's status replaced.
func (s StepSet) With(step Step, st StepStatus) StepSet {
	out := make(StepSet, len(s)+1)
	maps.Copy(out, s)
	if st.State == "" {
		st.State = StepPending
	}
	out[step] = st
	return out
}

// Ready reports whether a step may run: every step before it must be ok or skipped. A failed or
// still pending predecessor blocks it, and so does a step that is running. This is the strict
// order of the document steps written down as one function.
func (s StepSet) Ready(step Step) bool {
	for _, p := range PipelineSteps {
		if p == step {
			return true
		}
		if !p.IsDocument() {
			continue
		}
		switch s.State(p) {
		case StepOK, StepSkipped:
		default:
			return false
		}
	}
	return true
}
