package gateways

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/modelwindow"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// stepClock is a Clock the test moves by hand.
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }

// probeGateway records the calls it sees and answers the way the test asks it to.
type probeGateway struct {
	calls int
	err   error
	// waitForCtx makes the call block until its context ends, like a slow model.
	waitForCtx bool
}

func (g *probeGateway) Complete(ctx context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.calls++
	if g.waitForCtx {
		<-ctx.Done()
		return interfaces.AgentResponse{}, ctx.Err()
	}
	if g.err != nil {
		return interfaces.AgentResponse{}, g.err
	}
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{Content: "ok"}}, nil
}

func utc(hour, minute, second int) time.Time {
	return time.Date(2026, time.November, 19, hour, minute, second, 0, time.UTC)
}

func windowed(t *testing.T, gw interfaces.AgentGateway, clock interfaces.Clock) interfaces.AgentGateway {
	t.Helper()
	windows, err := modelwindow.Parse([]string{"01:00-04:00", "06:00-10:00"})
	if err != nil {
		t.Fatal(err)
	}
	return NewWindowedAgent(gw, windows, clock)
}

// TestWindowedAgentRefusesInsideAWindow: the call must not reach the endpoint at all, and the error
// must say when the derivation may continue.
func TestWindowedAgentRefusesInsideAWindow(t *testing.T) {
	inner := &probeGateway{}
	clock := &stepClock{now: utc(2, 30, 0)}
	gw := windowed(t, inner, clock)

	_, err := gw.Complete(context.Background(), interfaces.AgentRequest{})
	if !interfaces.IsWindowClosed(err) {
		t.Fatalf("err = %v, want the window deferral", err)
	}
	if inner.calls != 0 {
		t.Fatalf("the endpoint was called %d time(s) inside the window", inner.calls)
	}
	reopen, ok := interfaces.WindowReopen(err)
	if !ok || !reopen.Equal(utc(4, 0, 0)) {
		t.Fatalf("reopen = %s (%v), want 04:00", reopen, ok)
	}
}

// TestWindowedAgentPassesOutsideAWindow: the wrapper is transparent when the window is open.
func TestWindowedAgentPassesOutsideAWindow(t *testing.T) {
	inner := &probeGateway{}
	gw := windowed(t, inner, &stepClock{now: utc(12, 0, 0)})
	resp, err := gw.Complete(context.Background(), interfaces.AgentRequest{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if inner.calls != 1 || resp.Message.Content != "ok" {
		t.Fatalf("calls = %d, content = %q", inner.calls, resp.Message.Content)
	}
}

// TestWindowedAgentKeepsTheEndpointsFailure: a real failure is handed through untouched.
func TestWindowedAgentKeepsTheEndpointsFailure(t *testing.T) {
	boom := errors.New("boom")
	inner := &probeGateway{err: boom}
	gw := windowed(t, inner, &stepClock{now: utc(12, 0, 0)})
	if _, err := gw.Complete(context.Background(), interfaces.AgentRequest{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the endpoint's own failure", err)
	}
}

// TestWindowedAgentCutsAtTheBoundary: a call that is still running when the window opens ends there,
// and the result is a deferral, not a timeout.
func TestWindowedAgentCutsAtTheBoundary(t *testing.T) {
	inner := &probeGateway{waitForCtx: true}
	// 100 ms before the first window opens.
	clock := &stepClock{now: utc(0, 59, 59).Add(900 * time.Millisecond)}
	gw := windowed(t, inner, clock)

	started := time.Now()
	_, err := gw.Complete(context.Background(), interfaces.AgentRequest{})
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the call was not cut at the boundary (took %s)", elapsed)
	}
	if !interfaces.IsWindowClosed(err) {
		t.Fatalf("err = %v, want the window deferral", err)
	}
	reopen, _ := interfaces.WindowReopen(err)
	if !reopen.Equal(utc(4, 0, 0)) {
		t.Fatalf("reopen = %s, want 04:00", reopen)
	}
}

// TestWindowedAgentKeepsTheCallersDeadline: when the CALLER's context ends, that is a timeout, not
// the window — the distinction is what keeps a slow request from being requeued as "deferred".
func TestWindowedAgentKeepsTheCallersDeadline(t *testing.T) {
	inner := &probeGateway{waitForCtx: true}
	gw := windowed(t, inner, &stepClock{now: utc(12, 0, 0)})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := gw.Complete(ctx, interfaces.AgentRequest{})
	if err == nil {
		t.Fatal("want an error")
	}
	if interfaces.IsWindowClosed(err) {
		t.Fatalf("a caller timeout was reported as a window deferral: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
}

// TestNewWindowedAgentWithoutWindowsIsTransparent: no windows configured, no wrapper.
func TestNewWindowedAgentWithoutWindowsIsTransparent(t *testing.T) {
	inner := &probeGateway{}
	if got := NewWindowedAgent(inner, modelwindow.Windows{}, nil); got != inner {
		t.Fatal("an empty window set must return the gateway unchanged")
	}
	if got := NewWindowedAgent(nil, modelwindow.Windows{}, nil); got != nil {
		t.Fatal("no gateway must stay no gateway")
	}
}
