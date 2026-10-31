package gateways

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestDetectWall is the whole point of the detection: it must name a gate when the evidence is
// there and stay silent otherwise. A false positive is worse than a miss — it stops the app from
// reading a page that answered perfectly well.
func TestDetectWall(t *testing.T) {
	const html = "text/html; charset=utf-8"
	article := []byte(`<!doctype html><html><body>
		<nav>Sign in · Subscribe</nav>
		<h1>An article that is fine</h1>
		<p>It mentions login once, which is not a wall.</p>
	</body></html>`)
	loginPage := []byte(`<!doctype html><html><body>
		<h1>Sign in</h1>
		<form action="/session" method="post">
			<input type="email" name="email">
			<input type="password" name="password">
		</form>
	</body></html>`)
	// LWN (and many forums) put a login form in the header of every page while the article below
	// is fully readable. That is navigation, not a gate.
	articleWithHeaderLogin := []byte(`<!doctype html><html><body>
		<div id="menu"><form action="https://example.com/Login/" method="post">
			<input type="text" name="uname"><input type="password" name="pword">
			<input type="submit" value="Log in">
		</form></div>
		<main><h1>A fully readable article</h1>
		<p>` + strings.Repeat("This is the article text, and it is available. ", 20) + `</p>
		<form action="/comment" method="post"><input type="submit" value="Log in to post comments"></form>
		</main>
	</body></html>`)
	// Long navigation and footer text must not make a login page look like an article: the text
	// outside the chrome is still just the sign-in heading.
	loginWithLongChrome := []byte(`<!doctype html><html><body>
		<nav>` + strings.Repeat("menu item ", 100) + `</nav>
		<main><h1>Sign in</h1><form action="/session" method="post">
			<input type="password" name="password">
		</form></main>
		<footer>` + strings.Repeat("footer text ", 100) + `</footer>
	</body></html>`)
	paywall := []byte(`<!doctype html><html><body>
		<h1>The article</h1><p>The first paragraph is free.</p>
		<p>Subscribe to continue reading.</p>
	</body></html>`)

	tests := []struct {
		name        string
		status      int
		finalURL    string
		contentType string
		body        []byte
		want        entities.WallKind
	}{
		{name: "200 article", status: 200, finalURL: "https://example.com/a", contentType: html, body: article, want: entities.WallNone},
		{name: "401 is a login", status: 401, finalURL: "https://example.com/a", contentType: html, body: article, want: entities.WallLogin},
		{name: "403 is a login", status: 403, finalURL: "https://example.com/a", contentType: html, body: article, want: entities.WallLogin},
		{name: "redirect to signin path", status: 200, finalURL: "https://example.com/signin?next=/a", contentType: html, body: article, want: entities.WallLogin},
		{name: "redirect to login path", status: 200, finalURL: "https://example.com/login", contentType: html, body: article, want: entities.WallLogin},
		{name: "redirect to accounts host", status: 200, finalURL: "https://accounts.example.com/session", contentType: html, body: article, want: entities.WallLogin},
		{name: "password field", status: 200, finalURL: "https://example.com/a", contentType: html, body: loginPage, want: entities.WallLogin},
		{name: "password field in a readable article", status: 200, finalURL: "https://example.com/a", contentType: html, body: articleWithHeaderLogin, want: entities.WallNone},
		{name: "long chrome around a login page", status: 200, finalURL: "https://example.com/a", contentType: html, body: loginWithLongChrome, want: entities.WallLogin},
		{name: "paywall teaser", status: 200, finalURL: "https://example.com/a", contentType: html, body: paywall, want: entities.WallPaywall},
		{name: "consent host", status: 200, finalURL: "https://consent.google.com/ml?continue=x", contentType: html, body: article, want: entities.WallConsent},
		// A 404 is not a wall: the page is gone, which is a different report.
		{name: "404 is not a wall", status: 404, finalURL: "https://example.com/a", contentType: html, body: article, want: entities.WallNone},
		// A text/plain response is never scanned for HTML markers.
		{name: "plain text is not scanned", status: 200, finalURL: "https://example.com/a", contentType: "text/plain", body: paywall, want: entities.WallNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectWall(tt.status, tt.finalURL, tt.contentType, tt.body, 400); got != tt.want {
				t.Errorf("detectWall = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFetcherReportsTheWallAndTheFinalURL: the detection must be wired, not just written. The fetcher
// follows the redirect, so the FINAL url is the sign-in page — that is what tells a wall from an
// article that merely links to one.
func TestFetcherReportsTheWallAndTheFinalURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><form><input type="password"></form></body></html>`))
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer srv.Close()

	f := NewFetcher(FetcherConfig{UserAgent: "thought-keeper-test", AllowPrivate: true})
	page, err := f.Fetch(context.Background(), srv.URL+"/article")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if page.Wall != entities.WallLogin {
		t.Errorf("wall = %q, want login_required", page.Wall)
	}
	if page.FinalURL != srv.URL+"/login" {
		t.Errorf("final url = %q, want the sign-in page", page.FinalURL)
	}
}
