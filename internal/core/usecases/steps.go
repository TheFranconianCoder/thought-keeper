package usecases

import (
	"context"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// recordStep writes one document step's state for one source. Without a tracker it is a no-op, so
// a use case that has none (a test, a small CLI path) behaves exactly as before.
func recordStep(
	ctx context.Context,
	tracker interfaces.StepTracker,
	clock interfaces.Clock,
	hash string,
	step entities.Step,
	state entities.StepState,
	detail string,
	cause error,
) {
	if tracker == nil || hash == "" {
		return
	}
	st := entities.StepStatus{State: state, Detail: detail}
	if clock != nil {
		st.At = clock.Now()
	}
	if cause != nil {
		st.Error = cause.Error()
	}
	_ = tracker.Record(ctx, hash, step, st)
}
