package gateways

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/modelwindow"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// WindowedAgent is the AgentGateway of the DERIVATION work: every call is checked against the
// configured model windows, and a call still in flight when a window opens is cut at the boundary.
//
// It wraps the endpoint adapter; Ask and the assistant keep the raw gateway. The wrapper never hides
// a real endpoint failure: it converts exactly two situations into the window deferral — the window
// is already closed when the call would start, and the boundary cut a call the caller did not
// cancel.
type WindowedAgent struct {
	inner   interfaces.AgentGateway
	windows modelwindow.Windows
	clock   interfaces.Clock
}

// NewWindowedAgent wraps the gateway. An empty window set (or no gateway) returns the gateway as it
// is: an install without windows pays nothing for the rule.
func NewWindowedAgent(inner interfaces.AgentGateway, windows modelwindow.Windows, clock interfaces.Clock) interfaces.AgentGateway {
	if inner == nil || windows.Empty() {
		return inner
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &WindowedAgent{inner: inner, windows: windows, clock: clock}
}

// Complete refuses a call inside a window and cuts one at the window boundary. The work is deferred,
// not failed: the caller keeps what it already derived and resumes when the window closes.
func (a *WindowedAgent) Complete(ctx context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	now := a.clock.Now()
	if closed, reopen := a.windows.ClosedAt(now); closed {
		return interfaces.AgentResponse{}, interfaces.WindowClosed(reopen)
	}
	callCtx := ctx
	var reopen time.Time
	if boundary, ok := a.windows.NextClose(now); ok {
		// The endpoint's own patience (openai.timeout) is the inner gateway's business; this deadline
		// only says "not one second inside the window". It is expressed as the REMAINING DURATION,
		// not as an absolute instant: the clock that decides when the window opens is this struct's,
		// and a context deadline is measured against the wall clock. Handing the absolute instant
		// over made a call expire at once whenever the two disagreed — which they always do in a
		// test that moves the clock, and can do in a process whose clock was corrected.
		if remaining := boundary.Sub(now); remaining > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, remaining)
			defer cancel()
			if _, until := a.windows.ClosedAt(boundary); until.After(boundary) {
				reopen = until
			}
		}
	}
	resp, err := a.inner.Complete(callCtx, req)
	if err != nil && callCtx.Err() != nil && ctx.Err() == nil {
		// OUR deadline ended the call, not the caller's: the window opened while the model was
		// thinking. Defer it; the caller requeues the work and finishes it after the window.
		return interfaces.AgentResponse{}, interfaces.WindowClosed(reopen)
	}
	return resp, err
}
