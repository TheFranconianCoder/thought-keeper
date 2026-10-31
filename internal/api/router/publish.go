package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

// publishWrites tells every connected client that something changed, after a request that WROTE.
//
// The events the app publishes while it works name their container precisely, and the client that asked
// for a write also reloads what it knows. That leaves exactly one hole, and it is the one that matters:
// a write that is SYNCHRONOUS and quiet — a reindex from the CLI, a rebuild, a cleanup, a reindex in
// another window — changes the vault and the index while every open client keeps a stale picture until
// somebody reloads by hand. "The topics only appeared after a refresh" is that hole.
//
// So the net is coarse on purpose: any successful non-GET request outside the window channel says
// "sources, clusters, findings and the pipeline may have changed". The client COALESCES per type and re-reads
// each container once, so four events cost four cheap GETs — and no future operation can forget to
// announce itself, which is what a per-route list would guarantee the moment someone adds a route.
func publishWrites(hub *stream.Hub) func(http.Handler) http.Handler {
	if hub == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			// The window channel is the shell talking to itself; it changes no data. A command is a
			// READ that happens to write its own record: it announces itself with its own frame, and
			// reloading every container for it was a full list read behind every keystroke of the
			// assistant's field.
			if strings.Contains(r.URL.Path, "/window/") || strings.HasPrefix(r.URL.Path, "/api/v1/commands") {
				next.ServeHTTP(w, r)
				return
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			// Only a write that SUCCEEDED changed something. A rejected request (an invalid id, a
			// conflict) must not send the whole UI back to the server for nothing.
			if status := ww.Status(); status < 200 || status >= 300 {
				return
			}
			cause := r.Method + " " + r.URL.Path
			for _, typ := range []string{"source", "clusters", "findings", "pipeline", "wiki", "interests"} {
				hub.Publish(typ, map[string]any{"cause": cause})
			}
		})
	}
}
