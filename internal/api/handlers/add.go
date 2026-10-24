package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// maxUploadBytes caps a browser upload (drag & drop / file picker).
const maxUploadBytes = 100 << 20

// IntakeService stores a raw source and indexes it.
type IntakeService interface {
	Execute(ctx context.Context, in usecases.IntakeInput) (partialentities.PartialSource, error)
}

// URLService fetches a URL and stores it as a Markdown snapshot (or a bookmark), or expands it when it
// is a sitemap or a feed. ExecuteManual is the human's own addition: it WINS over a wiki/sitemap claim.
type URLService interface {
	Execute(ctx context.Context, url string, bookmark bool) (partialentities.PartialSource, error)
	ExecuteManual(ctx context.Context, url string, bookmark bool) (partialentities.PartialSource, error)
}

// AddHandler serves POST /api/v1/add. Three shapes are accepted:
//   - JSON {"path": "..."}      — a file already on the machine
//   - JSON {"url": "..."}       — fetch a page and snapshot it locally
//   - multipart/form-data       — a browser upload (drag & drop), field "file"
//
// Intake is fast (hashing, or one fetch), so it is synchronous; the slow, agent
// backed work happens later through a run.
type AddHandler struct {
	Intake IntakeService
	URL    URLService
	// OnAdded is called after a successful intake so the app can schedule tree
	// building off the request path.
	OnAdded func(hash string)
	// Children claims and fetches the pages a sitemap or a feed declared. It is background work: the
	// parent source is already stored when this is called.
	Children ChildScheduler
}

// ChildScheduler queues the pages a sitemap or feed source declared. The feed's pages are claimed the
// same way; the REFRESH is what differs (a sitemap is reconciled 1:1, a feed is only extended).
type ChildScheduler interface {
	ScheduleSitemapURLs(parentHash string, urls []string)
	ScheduleFeedURLs(parentHash string, urls []string)
}

// Post adds a source from a path, a URL or an upload.
func (h AddHandler) Post(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.upload(w, r)
		return
	}

	var req payloads.AddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	if url := strings.TrimSpace(req.URL); url != "" {
		if h.URL == nil {
			payloads.WriteError(w, types.New("not_implemented", "url intake is not configured", types.ErrNotImplemented))
			return
		}
		src, err := h.URL.ExecuteManual(r.Context(), url, req.Bookmark)
		if err != nil {
			payloads.WriteError(w, err)
			return
		}
		h.finish(w, src)
		return
	}
	if req.Path == "" {
		payloads.WriteError(w, types.New("invalid_body", "path or url is required", types.ErrInvalidURL))
		return
	}
	info, err := os.Stat(req.Path)
	if err != nil {
		payloads.WriteError(w, types.New("invalid_body", "cannot read path: "+err.Error(), err))
		return
	}
	if info.IsDir() {
		payloads.WriteError(w, types.New(
			"unsupported_source",
			"a folder is not a source: point at individual .md, .pdf or .txt files (a source is one immutable file)",
			types.ErrUnsupportedSource,
		))
		return
	}
	if !info.Mode().IsRegular() {
		payloads.WriteError(w, types.New("unsupported_source", "only regular files can be added", types.ErrUnsupportedSource))
		return
	}
	f, err := os.Open(req.Path)
	if err != nil {
		payloads.WriteError(w, types.New("invalid_body", "cannot open path: "+err.Error(), err))
		return
	}
	defer func() { _ = f.Close() }()

	src, err := h.Intake.Execute(r.Context(), usecases.IntakeInput{Name: filepath.Base(req.Path), Reader: f})
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	h.finish(w, src)
}

// upload handles a browser multipart upload.
func (h AddHandler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		payloads.WriteError(w, types.New("invalid_body", "no file field in the upload", types.ErrUnsupportedSource))
		return
	}
	defer func() { _ = file.Close() }()

	src, err := h.Intake.Execute(r.Context(), usecases.IntakeInput{Name: header.Filename, Reader: file})
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	h.finish(w, src)
}

func (h AddHandler) finish(w http.ResponseWriter, src partialentities.PartialSource) {
	w.Header().Set("Location", "/api/v1/sources/"+src.Hash)
	if h.OnAdded != nil {
		h.OnAdded(src.Hash)
	}
	if h.Children != nil {
		if len(src.SitemapURLs) > 0 {
			h.Children.ScheduleSitemapURLs(src.Hash, src.SitemapURLs)
		}
		if len(src.RSSURLs) > 0 {
			h.Children.ScheduleFeedURLs(src.Hash, src.RSSURLs)
		}
	}
	payloads.WriteJSON(w, http.StatusCreated, payloads.SourceFromPartial(src))
}
