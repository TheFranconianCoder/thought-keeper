package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// SourceLister lists indexed sources.
type SourceLister interface {
	Execute(ctx context.Context, filter entities.SourceFilter, limit, offset int) ([]partialentities.PartialSource, error)
}

// SourceCounter is the total behind a page. Nil means the page's length IS the total.
type SourceCounter interface {
	Count(ctx context.Context, filter entities.SourceFilter) (int, error)
}

// SourceFacetLister counts the Sources lens's facets: the tag cloud and attached snapshots.
type SourceFacetLister interface {
	Execute(ctx context.Context) (usecases.SourceFacetsResult, error)
}

// SourceGetter returns one source by hash.
type SourceGetter interface {
	Execute(ctx context.Context, hash string) (partialentities.PartialSource, error)
}

// SourceTagSetter replaces one source's human labels.
type SourceTagSetter interface {
	Execute(ctx context.Context, hash string, tags []string) (partialentities.PartialSource, error)
}

// SourceChildLister lists the sources one source owns (a sitemap's pages, a wiki page's URLs).
type SourceChildLister interface {
	Execute(ctx context.Context, hash string) (usecases.SourceChildrenResult, error)
}

// SourceDeleter removes a source and its orphaned atoms.
type SourceDeleter interface {
	Execute(ctx context.Context, hash string) (usecases.DeleteResult, error)
}

// URLAttacher stores a human-supplied snapshot for a URL the app could not read (a login wall, a
// paywall). The URL stays the source's address; the file becomes its snapshot.
type URLAttacher interface {
	Execute(ctx context.Context, in usecases.AttachInput) (partialentities.PartialSource, error)
}

// CommandSourceSet is an assistant result's materialized source set, in the command's order.
type CommandSourceSet interface {
	Execute(ctx context.Context, id ulid.ULID, limit, offset int) ([]partialentities.PartialSource, usecases.CommandWindow, error)
}

// SourceOpener opens a source's raw bytes for inline viewing, and its readable rendering — the
// Markdown step 1 wrote next to a source whose bytes are not readable (a PDF), which is what the
// overlay shows while the PDF itself stays one click away.
type SourceOpener interface {
	Execute(ctx context.Context, hash string) (io.ReadCloser, string, string, error)
	Rendered(ctx context.Context, hash string) (io.ReadCloser, string, string, error)
	// Sidecar opens one derived file by its fixed suffix (the Files view's names).
	Sidecar(ctx context.Context, hash, suffix string) (io.ReadCloser, string, string, error)
}

// FaviconOpener returns a source's stored icon.
type FaviconOpener interface {
	Execute(ctx context.Context, hash string) (io.ReadCloser, string, error)
}

// SummaryScheduler writes one source's overview from its claims. The summary is a run of its own,
// so a slow model never blocks the document that produced the claims.
type SummaryScheduler interface {
	ScheduleSummary(hash string) (string, error)
}

// DistillScheduler schedules a source's DOCUMENT run (outline if needed → distill).
type DistillScheduler interface {
	ScheduleDistill(hash string, reset bool) (string, error)
}

// SidecarLister lists the files a source consists of (its object and its sidecars).
type SidecarLister interface {
	Execute(ctx context.Context, hash string) ([]usecases.Sidecar, error)
}

// RunStatus reports which DOCUMENT step is running for a source right now (empty when none is).
// The CLUSTER step is not per source: it runs over the whole vault and is reported globally.
type RunStatus interface {
	RunningStep(hash string) string
}

// Runner is what the app provides: it schedules document runs/queries/lints and reports
// their state.
type Runner interface {
	// Reset throws a source's derivation away and rebuilds it (tree, claims, pages).
	Reset(hash string) (usecases.ResetResult, error)
	DistillScheduler
	SummaryScheduler
	CaptureScheduler
	RunStatus
	QueryScheduler
	LintScheduler
	VocabularyScheduler
	MaintenanceMarker
	ExclusiveWriter
	URLBulkScheduler
}

// MaintenanceMarker registers a background job the Running list should show: the passes that run
// synchronously behind a request (a reindex, a reconcile, a cleanup, a rebuild of the outlines, a URL
// refresh) can take a while, and "the button did nothing for two minutes" is what the list prevents.
type MaintenanceMarker interface {
	MarkMaintenance(label, target string) func()
}

// ExclusiveWriter runs a maintenance pass that writes the vault inside the exclusive gate. It waits
// for the document steps and is a job the reader sees while it waits.
type ExclusiveWriter interface {
	ExclusiveWrite(ctx context.Context, label string, fn func(context.Context) error) error
}

// Distill serves POST /api/v1/sources/{hash}/distill: it starts the source's DOCUMENT run (outline
// if the tree is missing → distill). The cluster step (compile) is not part of it — it runs over the
// whole vault and has its own endpoint.
func (h SourcesHandler) DistillPost(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	var req payloads.DistillRequest
	if r.Body != nil {
		// An empty body is the normal call (continue the derivation).
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	runID, err := h.Distill.ScheduleDistill(hash, req.Reset)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: string(entities.RunRunning)})
}

// CaptureScheduler re-runs step 1 for a source (its report says whether anything was scheduled).
type CaptureScheduler interface {
	ScheduleCapture(hash string) (bool, string, error)
}

// Capture serves POST /api/v1/sources/{hash}/capture: step 1 again (extract, render, name), and the
// outline follows — the steps stay in order.
func (h SourcesHandler) Capture(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	queued, detail, err := h.Capturer.ScheduleCapture(hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.CaptureResult{Queued: queued, Detail: detail})
}

// Attach serves POST /api/v1/sources/{hash}/attach: a human-supplied snapshot for a URL the app could
// not read. The body is multipart with one file in the `file` field. The URL stays the address of the
// source; the file becomes its snapshot and the document steps are re-armed.
func (h SourcesHandler) Attach(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Attacher == nil {
		payloads.WriteError(w, types.New("not_implemented", "attaching a snapshot is not configured", types.ErrNotImplemented))
		return
	}
	// The ceiling is the request's, before anything is parsed: the file is the reader's, but the
	// process still has to survive a wrong click.
	const maxBytes = 64 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "the attached file could not be read: "+err.Error(), types.ErrUnsupportedSource))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		payloads.WriteError(w, types.New("invalid_body", "attach one file in the `file` field", types.ErrUnsupportedSource))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		payloads.WriteError(w, types.New("invalid_body", "the attached file could not be read: "+err.Error(), types.ErrUnsupportedSource))
		return
	}
	src, err := h.Attacher.Execute(r.Context(), usecases.AttachInput{Hash: hash, Name: header.Filename, Data: data})
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnAttached != nil {
		h.OnAttached(hash)
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.SourceFromPartial(src))
}

// SourcesHandler serves the source endpoints.
type SourcesHandler struct {
	Lister SourceLister
	// Counter is the total behind the page, so the list can say "200 of 465" and load the rest.
	Counter SourceCounter
	// Facets is the tag cloud and the attached count (nil = no facets).
	Facets SourceFacetLister
	// ChildLister is the Sub-sources tab: the sources this one owns.
	ChildLister SourceChildLister
	// TagSetter edits the human tags of a source.
	TagSetter SourceTagSetter
	// Summary schedules the Summary tab's run (nil = the feature is not wired).
	Summary SummaryScheduler
	Getter  SourceGetter
	// Distill schedules a source's document run; Status says which step runs for it right now.
	Distill DistillScheduler
	Status  RunStatus
	// Capturer re-runs step 1 for one source; Sidecars lists the files it consists of.
	Capturer CaptureScheduler
	Sidecars SidecarLister
	Delete   SourceDeleter
	Content  SourceOpener
	Favicon  FaviconOpener
	// FixURLs2 repairs sources that lost the fact that they are URL snapshots.
	FixURLs2 URLKindFixer
	// Log reads the source's own log: everything that was done to it, in one place.
	Log SourceLogger
	// Reset throws the source's derivation away (tree, claims) and rebuilds it.
	Reset SourceResetter
	// Attacher fills a blocked URL source with a snapshot the human supplied.
	Attacher URLAttacher
	// OnAttached is called after a snapshot is attached, so the app can emit events and pump the
	// document steps (the source is readable again).
	OnAttached func(hash string)
	// OnRemoved is called after a source is deleted so the app can emit events.
	OnRemoved func(hash string)
	// Mark shows a synchronous pass in the Running list while it works.
	Mark MaintenanceMarker
	// Commands resolves an assistant result (?command=<id>) into rows. Nil means the address is
	// not understood here; the named criteria beside it still filter.
	Commands CommandSourceSet
}

// List serves GET /api/v1/sources.
func (h SourcesHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 200)
	offset := atoiDefault(q.Get("offset"), 0)

	// An assistant result is read from its RECORD, not from the filter: the set was materialized
	// when the command ran, so a source that arrived later is not in it and a deleted one simply
	// fails to resolve. A record that has been pruned leaves the named criteria in the address, and
	// the payload says so instead of pretending the list is the assistant's answer.
	handled, expired := h.serveCommand(w, r, limit, offset)
	if handled {
		return
	}
	filter := entities.SourceFilter{
		Kind:          strings.TrimSpace(q.Get("kind")),
		Query:         strings.TrimSpace(q.Get("q")),
		Tag:           strings.TrimSpace(q.Get("tag")),
		AttachedOnly:  truthy(q.Get("attached")),
		BookmarksOnly: truthy(q.Get("bookmarks")),
		FailedOnly:    truthy(q.Get("failed")),
	}
	srcs, err := h.Lister.Execute(r.Context(), filter, limit, offset)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := payloads.SourcesFromPartials(srcs)
	h.decorate(out)
	// Count is the TOTAL of the SAME filter, not the page: the reader shows it and the windowed
	// list uses it as the scroll height.
	total := len(out)
	if h.Counter != nil {
		if n, cErr := h.Counter.Count(r.Context(), filter); cErr == nil {
			total = n
		}
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.SourceList{Sources: out, Count: total, Total: total, Command: expired})
}

// serveCommand serves an assistant result. It reports whether it wrote the response, and — when the
// record named in the address no longer exists — the ref that marks the address as expired. A
// command for another lens is neither: the list is then simply the named filter, and saying
// "expired" about a record that is there would be a lie.
func (h SourcesHandler) serveCommand(w http.ResponseWriter, r *http.Request, limit, offset int) (bool, *payloads.CommandRef) {
	raw := strings.TrimSpace(r.URL.Query().Get("command"))
	if raw == "" || h.Commands == nil {
		return false, nil
	}
	id, err := ulid.ParseStrict(raw)
	if err != nil {
		return false, nil
	}
	srcs, window, cErr := h.Commands.Execute(r.Context(), id, limit, offset)
	if cErr != nil {
		if types.CodeOf(cErr) == "not_found" {
			return false, &payloads.CommandRef{ID: id.String(), Expired: true}
		}
		return false, nil
	}
	out := payloads.SourcesFromPartials(srcs)
	h.decorate(out)
	payloads.WriteJSON(w, http.StatusOK, payloads.SourceList{
		Sources: out, Count: window.Total, Total: window.Total,
		Command: &payloads.CommandRef{ID: id.String(), Total: window.Total, Missing: window.Missing},
	})
	return true, nil
}

// decorate adds the step that runs for a row RIGHT NOW. It is the live part of a row: the record's
// lights are the last write, the spinner is the present.
func (h SourcesHandler) decorate(out []payloads.Source) {
	if h.Distill == nil || h.Status == nil {
		return
	}
	for i := range out {
		out[i].RunningStep = h.Status.RunningStep(out[i].Hash)
	}
}

// Facets serves GET /api/v1/sources/facets: the Sources tag cloud and the attached count. It is
// read once per list view, never per window.
func (h SourcesHandler) ListFacets(w http.ResponseWriter, r *http.Request) {
	if h.Facets == nil {
		payloads.WriteJSON(w, http.StatusOK, payloads.SourceFacets{Tags: []payloads.SourceTagFacet{}})
		return
	}
	res, err := h.Facets.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := payloads.SourceFacets{Tags: make([]payloads.SourceTagFacet, 0, len(res.Tags)), Attached: res.Attached}
	for _, tag := range res.Tags {
		out.Tags = append(out.Tags, payloads.SourceTagFacet{Tag: tag.Name, Count: tag.Uses})
	}
	payloads.WriteJSON(w, http.StatusOK, out)
}

// SummaryText serves GET /api/v1/sources/{hash}/summary: the source's overview as Markdown. A
// source without one answers 404 — the tab shows its empty state, never an invented summary.
func (h SourcesHandler) SummaryText(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Content == nil {
		payloads.WriteError(w, types.New("not_implemented", "the source summary is not available", types.ErrNotImplemented))
		return
	}
	rc, _, _, err := h.Content.Sidecar(r.Context(), hash, entities.SidecarSummary)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// SummaryPost serves POST /api/v1/sources/{hash}/summary: it schedules the run that writes (or
// rewrites) the overview and answers 202 with its id.
func (h SourcesHandler) SummaryPost(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Summary == nil {
		payloads.WriteError(w, types.New("not_implemented", "the source summary is not available", types.ErrNotImplemented))
		return
	}
	runID, err := h.Summary.ScheduleSummary(hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusAccepted, payloads.DistillResponse{RunID: runID, Status: string(entities.RunRunning)})
}

// PutTags serves PUT /api/v1/sources/{hash}/tags: replace the source's human labels. The .meta record
// is the truth, the index mirrors it, and successful writes publish a source event (the router's
// write publisher), so every window re-reads the row.
func (h SourcesHandler) PutTags(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.TagSetter == nil {
		payloads.WriteError(w, types.New("not_implemented", "source tags are not available", types.ErrNotImplemented))
		return
	}
	var req payloads.SourceTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "the tags could not be read: "+err.Error(), types.ErrInvalidURL))
		return
	}
	src, err := h.TagSetter.Execute(r.Context(), hash, req.Tags)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.SourceFromPartial(src))
}

// Children serves GET /api/v1/sources/{hash}/children: the sources this one owns — a sitemap's
// pages, or the URLs a wiki page brought into the vault. It is a list, not a tree: ownership is one
// level deep by construction.
func (h SourcesHandler) Children(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.ChildLister == nil {
		payloads.WriteJSON(w, http.StatusOK, payloads.SourceList{Sources: []payloads.Source{}, Count: 0, Total: 0})
		return
	}
	res, err := h.ChildLister.Execute(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.SourceList{
		Sources: payloads.SourcesFromPartials(res.Sources), Count: res.Total, Total: res.Total,
	})
}

// SourceResetter resets one source's derivation.
type SourceResetter interface {
	Reset(hash string) (usecases.ResetResult, error)
}

// ResetSource serves POST /api/v1/sources/{hash}/reset.
func (h SourcesHandler) ResetSource(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Reset == nil {
		payloads.WriteError(w, types.New("not_implemented", "resetting is unavailable", types.ErrNotImplemented))
		return
	}
	res, err := h.Reset.Reset(hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.SourceReset{
		Hash: hash, Atoms: res.Atoms, Nodes: res.Nodes, Bookmark: res.Bookmark,
	})
}

// SourceLogger reads a source's log.
type SourceLogger interface {
	Read(ctx context.Context, hash string) (string, error)
}

// LogText serves GET /api/v1/sources/{hash}/log as plain text.
func (h SourcesHandler) LogText(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Log == nil {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("no log configured\n"))
		return
	}
	text, err := h.Log.Read(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(text))
}

// Get serves GET /api/v1/sources/{hash}.
func (h SourcesHandler) Get(w http.ResponseWriter, r *http.Request) {
	src, err := h.Getter.Execute(r.Context(), chi.URLParam(r, "hash"))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := payloads.SourceFromPartial(src)
	// The single read also lists the source's FILES (the object and its sidecars, with sizes): a
	// source is one folder entry with its whole story, and a missing file is exactly what the
	// consistency job repairs. The list does not do this — it would stat files per row.
	if h.Sidecars != nil {
		if cars, err := h.Sidecars.Execute(r.Context(), out.Hash); err == nil {
			out.Sidecars = make([]payloads.Sidecar, 0, len(cars))
			for _, c := range cars {
				out.Sidecars = append(out.Sidecars, payloads.Sidecar{
					Name: c.Name, Bytes: c.Bytes, Object: c.Name == out.Ext,
				})
			}
		}
	}
	payloads.WriteJSON(w, http.StatusOK, out)
}

// Delete serves DELETE /api/v1/sources/{hash}.
func (h SourcesHandler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if h.Delete == nil {
		payloads.WriteError(w, types.New("not_implemented", "delete is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.Delete.Execute(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	if h.OnRemoved != nil {
		h.OnRemoved(hash)
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.DeleteResult{Hash: hash, AtomsRemoved: res.AtomsRemoved, DependentsRemoved: res.DependentsRemoved})
}

// Favicon serves GET /api/v1/sources/{hash}/favicon.
func (h SourcesHandler) FaviconServe(w http.ResponseWriter, r *http.Request) {
	if h.Favicon == nil {
		http.NotFound(w, r)
		return
	}
	rc, contentType, err := h.Favicon.Execute(r.Context(), chi.URLParam(r, "hash"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// FixURLs serves POST /api/v1/fix-urls: repair sources whose index entry lost the fact that they are
// URL snapshots (the snapshot's own frontmatter says so).
func (h SourcesHandler) FixURLs(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("fix-urls", "")()
	}
	if h.FixURLs2 == nil {
		payloads.WriteError(w, types.New("not_implemented", "url repair is not available", types.ErrNotImplemented))
		return
	}
	res, err := h.FixURLs2.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, map[string]int{"checked": res.Checked, "fixed": res.Fixed})
}

// Raw serves GET /api/v1/sources/{hash}/raw: the snapshot's bytes inline.
func (h SourcesHandler) Raw(w http.ResponseWriter, r *http.Request) {
	if h.Content == nil {
		payloads.WriteError(w, types.New("not_implemented", "raw source is not available", types.ErrNotImplemented))
		return
	}
	hash := chi.URLParam(r, "hash")
	// `which` names what to serve: the object (default), the readable rendering, or one of the
	// derived files the Files view lists. Anything else is a 404 — the names are fixed, so a request
	// can never walk out of the source's folder.
	open := h.Content.Execute
	switch which := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("which"))); which {
	case "", "object":
	case "render":
		open = h.Content.Rendered
	default:
		suffix := which
		if !strings.HasPrefix(suffix, ".") {
			suffix = "." + suffix
		}
		switch suffix {
		case entities.SidecarMeta, entities.SidecarOutline, entities.SidecarSummary, entities.SidecarLog:
			open = func(ctx context.Context, hash string) (io.ReadCloser, string, string, error) {
				return h.Content.Sidecar(ctx, hash, suffix)
			}
		default:
			payloads.WriteError(w, types.New("not_found", "this source has no file named "+which, types.ErrNotFound))
			return
		}
	}
	rc, ext, title, err := open(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	defer func() { _ = rc.Close() }()

	w.Header().Set("Content-Type", contentTypeFor(ext))
	w.Header().Set("Content-Disposition", "inline; filename=\""+fileName(title, ext, chi.URLParam(r, "hash"))+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func contentTypeFor(ext string) string {
	switch strings.ToLower(ext) {
	case ".pdf":
		return "application/pdf"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func fileName(title, ext, hash string) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = hash
		if len(base) > 12 {
			base = base[:12]
		}
	}
	base = strings.NewReplacer("\"", "", "\\", "", "/", "-", "\n", " ").Replace(base)
	return base + ext
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// truthy reads the query-string spelling of a boolean filter. Only an explicit yes turns it on;
// an absent or malformed value leaves the filter out.
func truthy(s string) bool {
	return s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "yes")
}
