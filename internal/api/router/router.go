// Package router wires the chi router: the JSON API under /api/v1 and the embedded
// PWA for everything else (same origin — no CORS, no token).
package router

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/handlers"
	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Gate is the pipeline's ordering rule as HTTP middleware. A SHARED request may run beside the
// document steps; an EXCLUSIVE one rewrites the vault and waits for them (and for the cluster step).
// The router applies it to the synchronous write endpoints; the async workers take the same gate
// themselves. A nil Gate (tests) is a no-op.
type Gate interface {
	Shared(http.Handler) http.Handler
	Exclusive(http.Handler) http.Handler
}

// Deps are the handlers the router mounts.
type Deps struct {
	Status     handlers.StatusProvider
	Sources    handlers.SourcesHandler
	Add        handlers.AddHandler
	URL        handlers.URLHandler
	Reindex    handlers.ReindexHandler
	Vocabulary handlers.VocabularyHandler
	Tree       handlers.TreeHandler
	Atoms      handlers.AtomHandler
	Wiki       handlers.WikiHandler
	Search     handlers.SearchHandler
	Asks       handlers.AskHandler
	Query      handlers.QueryHandler
	Lint       handlers.LintHandler
	Events     handlers.SSEHandler
	// Pipeline serves the GLOBAL status: which of the four separated steps is running, and queues
	// the cluster step by hand.
	Pipeline  handlers.PipelineHandler
	Cluster   handlers.ClusterHandler
	Reconcile handlers.ReconcileHandler
	Gate      Gate
	Log       *slog.Logger
}

// noopGate is the fallback for a router built by hand (tests): every request passes.
type noopGate struct{}

func (noopGate) Shared(next http.Handler) http.Handler    { return next }
func (noopGate) Exclusive(next http.Handler) http.Handler { return next }

// New builds the application router. web serves the embedded PWA.
func New(deps Deps, web http.Handler) http.Handler {
	if deps.Gate == nil {
		deps.Gate = noopGate{}
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	if deps.Log != nil {
		r.Use(requestLogger(deps.Log))
	}
	// A write that is synchronous and quiet still changed something: tell every connected client, so no
	// window keeps a stale picture until somebody reloads by hand (see publishWrites).
	r.Use(publishWrites(deps.Events.Hub))

	r.Route("/api/v1", func(r chi.Router) {
		// The API is a local live view: a cached answer to "what is in my vault?" is a lie. This
		// also keeps a transient empty or 404 answer from being reused until a hard reload. The
		// favicon and SSE handlers override it with their own policy.
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, req)
			})
		})
		r.Get("/status", handlers.StatusHandler{Provider: deps.Status}.Get)
		// The global pipeline status: documents (capture/outline/distill) or the cluster step —
		// never both, which is the whole point of the strict separation.
		r.Get("/pipeline", deps.Pipeline.Get)
		// Step 4 by hand. Steps 1..3 are per source (add / rebuild-tree / distill); this is the one
		// step that belongs to the whole vault.
		r.Post("/cluster", deps.Cluster.Post)
		// The consistency job by hand: the same pass the periodic worker runs. It deletes claims, so
		// it is a vault write: exclusive, never beside a document step.
		r.With(deps.Gate.Exclusive).Post("/reconcile", deps.Reconcile.Post)
		r.Get("/sources", deps.Sources.List)
		// The Sources lens's facets: the tag cloud and the attached count. Static, before the
		// {hash} route, so a hash can never shadow it.
		r.Get("/sources/facets", deps.Sources.ListFacets)
		r.Get("/sources/{hash}", deps.Sources.Get)
		r.Get("/sources/{hash}/tree", deps.Tree.Get)
		// The Sub-sources tab: sitemap pages or a wiki page's linked URLs.
		r.Get("/sources/{hash}/children", deps.Sources.Children)
		// Human labels on a source; the source-tags vocabulary observes them.
		r.With(deps.Gate.Shared).Put("/sources/{hash}/tags", deps.Sources.PutTags)
		// The Summary tab: the derived overview, and the run that writes it.
		r.Get("/sources/{hash}/summary", deps.Sources.SummaryText)
		r.Post("/sources/{hash}/summary", deps.Sources.SummaryPost)
		// The DOCUMENT run of one source: outline if needed → distill. (The cluster step has its own
		// endpoint below: step 4 runs over the whole vault, never per source.)
		r.Post("/sources/{hash}/distill", deps.Sources.DistillPost)
		// Step 1 again (extract, render, name) — the outline follows, so the order holds.
		r.Post("/sources/{hash}/capture", deps.Sources.Capture)
		// Refresh ONE URL source now: a feed or a sitemap re-expands its list, a page is re-fetched.
		r.With(deps.Gate.Shared).Post("/sources/{hash}/refresh", deps.URL.PostOne)
		// Try the URL again even when its snapshot was attached by hand. It may replace the snapshot
		// and reset the derivation (claims and tree), so it takes the exclusive side.
		r.With(deps.Gate.Exclusive).Post("/sources/{hash}/refetch", deps.URL.Refetch)
		// A human-supplied snapshot for a URL the app could not read (a login wall, a paywall). The
		// URL stays the address; the file becomes the snapshot. Replacing one resets the old
		// derivation, so the write is exclusive.
		r.With(deps.Gate.Exclusive).Post("/sources/{hash}/attach", deps.Sources.Attach)
		// The single-source outline writes the tree rows and can correct a derivation: exclusive.
		r.With(deps.Gate.Exclusive).Post("/sources/{hash}/rebuild-tree", deps.Tree.RebuildOne)
		// A reset deletes claims, tree rows and part records: exclusive, so it cannot race a run.
		r.With(deps.Gate.Exclusive).Post("/sources/{hash}/reset", deps.Sources.ResetSource)
		r.Get("/sources/{hash}/nodes/{node}", deps.Tree.GetNode)
		r.Get("/sources/{hash}/atoms", deps.Atoms.ListSourceAtoms)
		r.With(deps.Gate.Shared).Post("/fix-urls", deps.Sources.FixURLs)
		r.Get("/sources/{hash}/raw", deps.Sources.Raw)
		r.Get("/sources/{hash}/log", deps.Sources.LogText)
		r.Get("/sources/{hash}/favicon", deps.Sources.FaviconServe)
		r.With(deps.Gate.Exclusive).Delete("/sources/{hash}", deps.Sources.DeleteSource)
		r.With(deps.Gate.Shared).Post("/add", deps.Add.Post)
		r.Post("/add-urls", deps.URL.PostMany)
		r.With(deps.Gate.Exclusive).Post("/reindex", deps.Reindex.Post)
		r.With(deps.Gate.Exclusive).Post("/rebuild-trees", deps.Tree.Rebuild)
		r.With(deps.Gate.Shared).Post("/refresh-urls", deps.URL.Post)
		r.Get("/urls/addresses", deps.URL.GetAddresses)
		r.With(deps.Gate.Exclusive).Post("/urls/migrate", deps.URL.PostMigrate)

		r.Get("/vocabulary", deps.Vocabulary.Get)
		r.Post("/vocabulary/curate", deps.Vocabulary.Curate)
		r.Post("/vocabulary/retopic", deps.Vocabulary.Rebranch)
		r.Post("/vocabulary/rebranch", deps.Vocabulary.Rebranch)
		// Wiki pages are user-authored Markdown. A save writes the page and its source
		// representation; the old derivation is cleared and the pipeline is pumped in the
		// background, so a save never waits for the gate.
		r.Get("/wiki", deps.Wiki.ListPages)
		r.Get("/wiki/{id}", deps.Wiki.GetPage)
		r.Post("/wiki", deps.Wiki.Create)
		r.Put("/wiki/{id}", deps.Wiki.Update)
		r.With(deps.Gate.Exclusive).Delete("/wiki/{id}", deps.Wiki.DeletePage)
		r.Get("/topics/claims", deps.Atoms.ListClaimTopics)
		r.Get("/atoms", deps.Atoms.ListAtoms)
		// The claim LAYOUT: what a vault still holds in the old branch folders (GET) and the move to
		// the ID layout (POST). A button, not a start-up habit — the vault is the user's.
		r.Get("/atoms/layout", deps.Atoms.GetAtomLayout)
		r.With(deps.Gate.Exclusive).Post("/atoms/reorganize", deps.Atoms.PostAtomReorganize)
		// The same claim written more than once: what is there (GET) and the collapse (POST).
		r.Get("/atoms/duplicates", deps.Atoms.GetAtomDuplicates)
		r.With(deps.Gate.Exclusive).Post("/atoms/collapse", deps.Atoms.PostAtomCollapse)
		// One-time repair: a body written while a claim body was still parsed for links carries its
		// quoted bracket syntax bare. It becomes inline code; it never becomes an edge.
		r.With(deps.Gate.Exclusive).Post("/atoms/quote-code", deps.Atoms.PostQuoteCode)
		r.Get("/atoms/{id}", deps.Atoms.Get)
		r.Get("/atoms/{id}/related", deps.Atoms.GetRelated)
		// The claim's cluster PEERS. There is no cluster view: a cluster is an association, not a
		// destination, and this is the only place it becomes visible.
		r.Get("/atoms/{id}/cluster", deps.Atoms.GetRelated)

		r.Get("/runs/{id}", deps.Atoms.GetRun)

		r.Get("/asks", deps.Asks.List)
		r.Get("/asks/{id}", deps.Asks.GetOne)
		r.Delete("/asks/{id}", deps.Asks.DeleteOne)

		r.Get("/search", deps.Search.Get)
		r.Post("/query", deps.Query.Post)
		r.Post("/lint", deps.Lint.Post)
		r.With(deps.Gate.Exclusive).Post("/cleanup", deps.Lint.CleanupPost)
		r.Get("/lint/findings", deps.Lint.ListFindings)
		r.Get("/events", deps.Events.Stream)
	})

	// Everything that is not the API is served by the static PWA handler. The UI
	// uses hash routing, so the handler returns real 404s for unknown files (no SPA
	// fallback); API misses get a JSON error.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			payloads.WriteError(w, types.ErrNotFound)
			return
		}
		web.ServeHTTP(w, req)
	})

	return r
}

// requestLogger logs writes and failed requests at info, everything else at debug.
//
// A successful read is not an event. The PWA is event-driven and re-reads a container whenever the
// server says it changed, so logging reads at info turned a pipeline run into a wall of HTTP lines
// that hid the run itself. The SSE connection is the exception: a stream that opens and closes is a
// disconnect worth seeing.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			level := slog.LevelInfo
			p := r.URL.Path
			read := r.Method == http.MethodGet || r.Method == http.MethodHead
			// A favicon that does not exist is expected for a source without one; the browser asks
			// for it once per row and falls back to a glyph.
			missingFavicon := read && strings.HasSuffix(p, "/favicon") && ww.Status() == http.StatusNotFound
			switch {
			case !strings.HasPrefix(p, "/api/"):
				level = slog.LevelDebug // static assets
			case read && p != "/api/v1/events" && (ww.Status() < http.StatusBadRequest || missingFavicon):
				level = slog.LevelDebug // a successful read, not a change
			}
			log.Log(r.Context(), level, "http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"dur", time.Since(start).Round(time.Millisecond).String(),
			)
		})
	}
}
