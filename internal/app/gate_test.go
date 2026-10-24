package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestExclusiveGateWaitsForDocument: an exclusive write must not start while a document step holds
// the shared side. This is the invariant the maintenance buttons used to bypass.
func TestExclusiveGateWaitsForDocument(t *testing.T) {
	a := &App{}
	started := make(chan struct{})
	done := make(chan struct{})
	h := a.Exclusive(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		close(done)
	}))

	a.gate.RLock()
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	select {
	case <-started:
		t.Fatal("an exclusive write started beside a document step")
	case <-time.After(80 * time.Millisecond):
	}
	a.gate.RUnlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the exclusive write never started after the shared side was released")
	}
}

// TestSharedGateRunsBesideDocument: two document-side writes may run at the same time; the cluster step
// is what has to wait for them.
func TestSharedGateRunsBesideDocument(t *testing.T) {
	a := &App{}
	ran := make(chan struct{})
	h := a.Shared(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(ran) }))

	a.gate.RLock()
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("a shared write waited for another shared write")
	}
	a.gate.RUnlock()
}
