package gateways

import (
	"net/url"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// detectWall names, from evidence, why a response is not the page the URL addresses. It is a short
// list of markers, deliberately: a heuristic that guesses turns an article that happens to mention
// "sign in" into a blocked source.
//
// The order is the strength of the evidence: an HTTP status or a redirect to a sign-in is proof; a
// password field is near-proof (a login form in the returned document); the paywall markers are the
// weakest, so they are checked last and only on a readable page.
func detectWall(status int, finalURL, contentType string, body []byte) entities.WallKind {
	if status == 401 || status == 403 {
		return entities.WallLogin
	}
	if u, err := url.Parse(finalURL); err == nil {
		if isLoginLocation(u) {
			return entities.WallLogin
		}
		if isConsentHost(u.Hostname()) {
			return entities.WallConsent
		}
	}
	if !isHTML(contentType) {
		return entities.WallNone
	}
	// The markers live near the top of a document; a 5 MiB body must not be scanned to find a form.
	head := body
	const headBytes = 512 << 10
	if len(head) > headBytes {
		head = head[:headBytes]
	}
	lower := strings.ToLower(string(head))
	if hasPasswordField(lower) {
		return entities.WallLogin
	}
	for _, marker := range paywallMarkers {
		if strings.Contains(lower, marker) {
			return entities.WallPaywall
		}
	}
	return entities.WallNone
}

// paywallMarkers are phrases a gated article's teaser carries and an open article does not.
var paywallMarkers = []string{
	"subscribe to continue reading",
	"subscribe to continue",
	"to continue reading, subscribe",
	"this article is for subscribers",
	"this content is for subscribers",
	"already a subscriber?",
	"subscribe now to read",
}

// hasPasswordField reports whether the document carries a password input. A login form is the one
// reliable sign that the page we got is a gate: articles do not ask for a password.
func hasPasswordField(lowerHTML string) bool {
	for _, attr := range []string{`type="password"`, `type='password'`, `type=password`} {
		if strings.Contains(lowerHTML, attr) {
			return true
		}
	}
	return false
}

// isLoginLocation reports whether a URL is a sign-in page: its host or path says so.
func isLoginLocation(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	for _, prefix := range []string{"login.", "signin.", "accounts.", "auth.", "sso."} {
		if strings.HasPrefix(host, prefix) {
			return true
		}
	}
	path := strings.ToLower(strings.TrimSuffix(u.Path, "/"))
	for _, marker := range []string{"/login", "/signin", "/sign-in", "/log-in", "/auth", "/sso", "/session/new"} {
		if path == marker || strings.HasSuffix(path, marker) || strings.Contains(path, marker+"/") {
			return true
		}
	}
	return false
}

// isConsentHost reports whether the request was redirected to a cookie/consent service — the other
// wall a public page hides behind.
func isConsentHost(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range []string{"consent.google.com", "consent.youtube.com", "consent.yahoo.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func isHTML(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml")
}
