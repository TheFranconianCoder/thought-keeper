package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

type pageLister struct {
	all        []partialentities.PartialSource
	lastL      int
	lastO      int
	lastFilter entities.SourceFilter
}

func (p *pageLister) Execute(_ context.Context, filter entities.SourceFilter, limit, offset int) ([]partialentities.PartialSource, error) {
	p.lastL, p.lastO, p.lastFilter = limit, offset, filter
	if offset > len(p.all) {
		return nil, nil
	}
	end := min(offset+limit, len(p.all))
	return p.all[offset:end], nil
}

type totalCounter struct{ n int }

func (c totalCounter) Count(context.Context, entities.SourceFilter) (int, error) { return c.n, nil }

// TestSourceListSaysThereIsMore: the list answers a PAGE, and `count` is the TOTAL — not the page's
// length. The store used to page on len(sources), so the 201st source was invisible; the label said
// "200 sources" while the vault held 465.
func TestSourceListSaysThereIsMore(t *testing.T) {
	all := make([]partialentities.PartialSource, 0, 3)
	for i, title := range []string{"one", "two", "three"} {
		all = append(all, partialentities.PartialSource{Hash: string(rune('a' + i)), Title: title})
	}
	lister := &pageLister{all: all}
	h := SourcesHandler{Lister: lister.Execute, Counter: totalCounter{n: 3}.Count}

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/sources?limit=2&offset=0", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got payloads.SourceList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 2 {
		t.Errorf("sources = %d, want one page of 2", len(got.Sources))
	}
	if got.Count != 3 || got.Total != 3 {
		t.Errorf("count/total = %d/%d, want the vault's 3", got.Count, got.Total)
	}

	// The second page starts where the first ended.
	req = httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/sources?limit=2&offset=2", nil)
	rec = httptest.NewRecorder()
	h.List(rec, req)
	if lister.lastO != 2 || lister.lastL != 2 {
		t.Errorf("page request = limit %d offset %d, want 2/2", lister.lastL, lister.lastO)
	}
}

// TestSourceListParsesTheWindowFilter: every filter the Sources window sends is part of both the
// page read and the count; a filter that only reached the page would make the scroll height lie.
func TestSourceListParsesTheWindowFilter(t *testing.T) {
	lister := &pageLister{}
	h := SourcesHandler{Lister: lister.Execute, Counter: totalCounter{n: 0}.Count}

	req := httptest.NewRequestWithContext(
		context.Background(),
		"GET",
		"/api/v1/sources?kind=pdf&q=rust&tag=systems&attached=1&bookmarks=1&failed=1&roots=1&limit=50&offset=100",
		nil,
	)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	want := entities.SourceFilter{
		Kind: "pdf", Query: "rust", Tag: "systems",
		AttachedOnly: true, BookmarksOnly: true, FailedOnly: true, RootsOnly: true,
	}
	if lister.lastFilter != want {
		t.Errorf("filter = %+v, want %+v", lister.lastFilter, want)
	}
	if lister.lastL != 50 || lister.lastO != 100 {
		t.Errorf("page = limit %d offset %d, want 50/100", lister.lastL, lister.lastO)
	}
}

// TestSourceResolveLinksReadsEveryURL: the resolver receives every `url` parameter of the request,
// in order, and the answer carries only what matched. A link destination may contain a comma, so
// the URLs cannot be joined into one value.
func TestSourceResolveLinksReadsEveryURL(t *testing.T) {
	var got []string
	h := SourcesHandler{LinkResolver: func(_ context.Context, urls []string) ([]entities.SourceLink, error) {
		got = urls
		return []entities.SourceLink{{URL: urls[0], Hash: "abc"}}, nil
	}}
	req := httptest.NewRequestWithContext(
		context.Background(),
		"GET",
		"/api/v1/sources/resolve?url=https%3A%2F%2Fa.example%2Fx%2Cy&url=https%3A%2F%2Fb.example%2Fz",
		nil,
	)
	rec := httptest.NewRecorder()
	h.ResolveLinks(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(got) != 2 || got[0] != "https://a.example/x,y" || got[1] != "https://b.example/z" {
		t.Fatalf("urls = %v, want both query values in order", got)
	}
	var out payloads.SourceLinkList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Links) != 1 || out.Links[0].Hash != "abc" {
		t.Errorf("links = %+v, want the resolver's one match", out.Links)
	}
}

type summaryOpener struct {
	text string
	err  error
}

func (o summaryOpener) Execute(context.Context, string) (io.ReadCloser, string, string, error) {
	return nil, "", "", types.New("not_found", "no object in this test", types.ErrNotFound)
}

func (o summaryOpener) Rendered(context.Context, string) (io.ReadCloser, string, string, error) {
	return nil, "", "", types.New("not_found", "no rendering in this test", types.ErrNotFound)
}

func (o summaryOpener) Sidecar(_ context.Context, _, suffix string) (io.ReadCloser, string, string, error) {
	if o.err != nil {
		return nil, "", "", o.err
	}
	if suffix != entities.SidecarSummary {
		return nil, "", "", types.New("not_found", "this test only serves the summary", types.ErrNotFound)
	}
	return io.NopCloser(strings.NewReader(o.text)), entities.SidecarSummary, "source", nil
}

type summaryScheduler struct {
	runID string
	err   error
}

func (s summaryScheduler) ScheduleSummary(string) (string, error) { return s.runID, s.err }

// TestSourceSummaryEndpoints: the Summary tab reads the sidecar as Markdown and asks for a run when it
// is missing. A source without a summary must answer 404 — the tab shows its empty state, it never
// invents prose.
func TestSourceSummaryEndpoints(t *testing.T) {
	const hash = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	withHash := func(req *http.Request) *http.Request {
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("hash", hash)
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	}

	h := SourcesHandler{
		Sidecar: summaryOpener{text: "A short overview.\n"}.Sidecar,
		Summary: summaryScheduler{runID: "01J8RUN"},
	}
	rec := httptest.NewRecorder()
	h.SummaryText(rec, withHash(httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/sources/"+hash+"/summary", nil)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "short overview") {
		t.Fatalf("summary = %d %q, want the sidecar", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("content type = %q, want Markdown", ct)
	}

	rec = httptest.NewRecorder()
	h.Sidecar = summaryOpener{err: types.New("not_found", "no summary yet", types.ErrNotFound)}.Sidecar
	h.SummaryText(rec, withHash(httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/sources/"+hash+"/summary", nil)))
	if rec.Code != 404 {
		t.Errorf("missing summary = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.SummaryPost(rec, withHash(httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/sources/"+hash+"/summary", nil)))
	if rec.Code != 202 {
		t.Fatalf("summary run = %d, want 202", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["run_id"] != "01J8RUN" {
		t.Errorf("run id = %v, want the scheduler's", body["run_id"])
	}
}

type tagSetter struct {
	got []string
}

func (t *tagSetter) Execute(_ context.Context, hash string, tags []string) (partialentities.PartialSource, error) {
	t.got = tags
	return partialentities.PartialSource{Hash: hash, Tags: tags, Title: "A source"}, nil
}

// TestSourceTagsEndpoint: the rail's editor is one PUT; the answer is the source with the labels, so
// the tab updates without waiting for the event stream.
func TestSourceTagsEndpoint(t *testing.T) {
	const hash = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	setter := &tagSetter{}
	h := SourcesHandler{TagSetter: setter.Execute}
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/sources/"+hash+"/tags",
		strings.NewReader(`{"tags":["go","caching"]}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("hash", hash)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.PutTags(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(setter.got) != 2 || setter.got[0] != "go" {
		t.Fatalf("tags passed = %v, want the body's labels", setter.got)
	}
	var got payloads.Source
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Tags) != 2 || got.Tags[1] != "caching" {
		t.Fatalf("response tags = %v, want the labels", got.Tags)
	}
}

type attachRecorder struct {
	got    usecases.AttachInput
	failOn error
}

func (a *attachRecorder) Execute(_ context.Context, in usecases.AttachInput) (partialentities.PartialSource, error) {
	a.got = in
	if a.failOn != nil {
		return partialentities.PartialSource{}, a.failOn
	}
	return partialentities.PartialSource{Hash: in.Hash, Kind: "url", Title: "The Page", Ext: ".md"}, nil
}

// TestAttachEndpoint: the rail's file picker is one multipart POST. The handler must read the `file`
// field, hand the bytes to the use case with the file's own name, and announce the source so the
// pipeline re-derives it.
func TestAttachEndpoint(t *testing.T) {
	const hash = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	attacher := &attachRecorder{}
	notified := ""
	h := SourcesHandler{
		Attacher:   attacher.Execute,
		OnAttached: func(h string) { notified = h },
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "saved-page.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("<html><body>the page</body></html>")); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/sources/"+hash+"/attach", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("hash", hash)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.Attach(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if attacher.got.Hash != hash || attacher.got.Name != "saved-page.html" {
		t.Errorf("attach input = %+v, want the hash and the file's name", attacher.got)
	}
	if !strings.Contains(string(attacher.got.Data), "the page") {
		t.Errorf("data = %q, want the uploaded bytes", attacher.got.Data)
	}
	if notified != hash {
		t.Errorf("OnAttached = %q, want the source's hash", notified)
	}
	var got payloads.Source
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "The Page" {
		t.Errorf("source title = %q", got.Title)
	}
}

// TestSourceListParsesTheArrivalWindow: `since` is an absolute instant on the filter — never a
// duration the server could interpret differently tomorrow — and a malformed one is refused instead
// of silently read as "no window". A queue read also carries the queue's own split, so the Start
// line needs no tag cloud for two numbers.
func TestSourceListParsesTheArrivalWindow(t *testing.T) {
	lister := &pageLister{}
	h := SourcesHandler{
		Lister: lister.Execute, Counter: totalCounter{n: 0}.Count,
		Queue: func(context.Context) (usecases.QueueCounts, error) {
			return usecases.QueueCounts{Open: 12, FromInterests: 8}, nil
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/sources?readlater=1&since=2023-11-14T00:00:00Z&limit=8", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	want, err := time.Parse(time.RFC3339, "2023-11-14T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !lister.lastFilter.Since.Equal(want) {
		t.Errorf("since = %s, want the instant the address named", lister.lastFilter.Since)
	}
	if !lister.lastFilter.ReadLaterOnly || lister.lastL != 8 {
		t.Errorf("filter = %+v limit %d, want the queue read of 8", lister.lastFilter, lister.lastL)
	}
	var got payloads.SourceList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Queue == nil || got.Queue.Open != 12 || got.Queue.FromInterests != 8 {
		t.Fatalf("queue = %+v, want 12 open / 8 from interests", got.Queue)
	}

	// The same filter without the queue carries no split: the numbers describe THE QUEUE.
	req = httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/sources?limit=8", nil)
	rec = httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var plain payloads.SourceList
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if plain.Queue != nil {
		t.Errorf("queue = %+v, want none outside the queue", plain.Queue)
	}

	// A duration is not an instant: the address must mean the same window tomorrow.
	req = httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/sources?since=7d", nil)
	rec = httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a duration", rec.Code)
	}
}
