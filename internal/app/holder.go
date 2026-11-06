package app

import (
	"net/http"
	"sync/atomic"

	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
)

// handlerHolder lets the HTTP server keep one handler while the router behind it is replaced on a
// config apply. A request that already loaded the old handler finishes on it; the next one sees the
// new wiring.
type handlerHolder struct{ p atomic.Pointer[http.Handler] }

func (h *handlerHolder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p := h.p.Load(); p != nil {
		(*p).ServeHTTP(w, r)
		return
	}
	http.Error(w, "the app is starting", http.StatusServiceUnavailable)
}

func (h *handlerHolder) Store(next http.Handler) { h.p.Store(&next) }

// deps is the current wiring. It is immutable once stored: an apply builds a new one and swaps the
// pointer, so an in-flight run keeps the use cases it started with.
func (a *App) deps() *registry.Deps {
	if p := a.depsPtr.Load(); p != nil {
		return p
	}
	// A test (or the moment before New publishes the first wiring) has no use cases yet. An empty
	// Deps keeps a nil logger or source log a nil check away instead of a nil-pointer panic.
	return &registry.Deps{}
}

// conf is the running configuration. The same rule as deps(): a value is never mutated in place.
func (a *App) conf() config.Config {
	if p := a.confPtr.Load(); p != nil {
		return *p
	}
	return config.Defaults()
}

// setConfig publishes a configuration. Only New and a config apply call it.
func (a *App) setConfig(cfg config.Config) { a.confPtr.Store(&cfg) }
