package entities_test

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestStepSetReadyIsTheStrictOrder: a step may only run when every step before it is ok (or does
// not apply). This is the whole point of the four steps being separated — a capture that failed
// cannot produce an honest outline, and an outline that is missing cannot be distilled into atoms
// that cite a node.
func TestStepSetReadyIsTheStrictOrder(t *testing.T) {
	set := entities.NewStepSet()
	if !set.Ready(entities.StepCapture) {
		t.Error("capture must be allowed to start")
	}
	if set.Ready(entities.StepOutline) {
		t.Error("outline must wait for capture")
	}

	set = set.With(entities.StepCapture, entities.StepStatus{State: entities.StepOK})
	if !set.Ready(entities.StepOutline) {
		t.Error("outline must be allowed once capture is ok")
	}
	if set.Ready(entities.StepDistill) {
		t.Error("distill must wait for the outline")
	}

	set = set.With(entities.StepOutline, entities.StepStatus{State: entities.StepFailed})
	if set.Ready(entities.StepDistill) {
		t.Error("a FAILED outline must block distill")
	}

	set = set.With(entities.StepOutline, entities.StepStatus{State: entities.StepOK})
	set = set.With(entities.StepDistill, entities.StepStatus{State: entities.StepOK})
	if !set.Ready(entities.StepCluster) {
		t.Error("the cluster step must be allowed once the document steps are done")
	}
}

// TestStepSetDefaultsToPending: a step nobody recorded is pending, never "done" and never unknown —
// an older vault shows four empty circles instead of a broken light.
func TestStepSetDefaultsToPending(t *testing.T) {
	var set entities.StepSet
	if got := set.State(entities.StepCapture); got != entities.StepPending {
		t.Errorf("state = %q, want pending", got)
	}
	with := set.With(entities.StepCapture, entities.StepStatus{Detail: "no state given"})
	if got := with.State(entities.StepCapture); got != entities.StepPending {
		t.Errorf("state = %q, want pending for an status without a state", got)
	}
	if got := with.State(entities.StepOutline); got != entities.StepPending {
		t.Errorf("unrecorded step = %q, want pending", got)
	}
}

// TestStepsAreDocumentStepsAndOneClusterStep pins the classification the whole design rests on.
func TestStepsAreDocumentStepsAndOneClusterStep(t *testing.T) {
	for _, step := range entities.PipelineSteps {
		want := step != entities.StepCluster
		if step.IsDocument() != want {
			t.Errorf("%s.IsDocument() = %v, want %v", step, step.IsDocument(), want)
		}
		if !step.Valid() {
			t.Errorf("%s is not valid", step)
		}
	}
	// The vocabulary of the steps is fixed: the names of the intermediate stages that led here
	// ("atomize", "tree", "intake") are not steps any more and must not be accepted.
	for _, gone := range []entities.Step{"atomize", "tree", "intake", "ingest"} {
		if gone.Valid() {
			t.Errorf("%q must not be a valid step any more", gone)
		}
	}
}
