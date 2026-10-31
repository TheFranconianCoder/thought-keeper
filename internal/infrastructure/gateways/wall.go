package gateways

import (
	"bytes"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// detectWall names, from evidence, why a response is not the page the URL addresses. It is a short
// list of markers, deliberately: a heuristic that guesses turns an article that happens to mention
// "sign in" into a blocked source.
//
// The order is the strength of the evidence: an HTTP status or a redirect to a sign-in is proof; a
// password field is near-proof, BUT only when the document does not carry a real article (many sites
// put a login form in the header of every page — LWN does); the paywall markers are the weakest, so
// they are checked last and only when the page has no substantial article text.
func detectWall(status int, finalURL, contentType string, body []byte, minChars int) entities.WallKind {
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
	hasPassword := hasPasswordField(lower)
	hasPaywall := hasPaywallMarker(lower)
	if !hasPassword && !hasPaywall {
		return entities.WallNone
	}
	if minChars <= 0 {
		minChars = 400
	}
	// A login form in the header of a real article is navigation, not a gate. So is a "subscribe"
	// footer. The page is a wall only when the document does not carry a readable article of its
	// own: a false positive (blocking a page that answers with its article) is worse than a miss.
	if articleTextChars(head, minChars) >= minChars {
		return entities.WallNone
	}
	if hasPassword {
		return entities.WallLogin
	}
	return entities.WallPaywall
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

func hasPaywallMarker(lowerHTML string) bool {
	for _, marker := range paywallMarkers {
		if strings.Contains(lowerHTML, marker) {
			return true
		}
	}
	return false
}

// hasPasswordField reports whether the document carries a password input. On its own this is not a
// wall: the header of an ordinary article often has a login form. It only counts together with a
// document that has no substantial article text of its own (see detectWall).
func hasPasswordField(lowerHTML string) bool {
	for _, attr := range []string{`type="password"`, `type='password'`, `type=password`} {
		if strings.Contains(lowerHTML, attr) {
			return true
		}
	}
	return false
}

// wallBoilerplate are the elements whose text is chrome, not article: a login form, a menu, a
// footer, a consent strip. Their text must not make a login page look like an article.
var wallBoilerplate = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"nav": true, "header": true, "footer": true, "aside": true, "form": true,
}

// articleTextChars measures the readable text of a document OUTSIDE navigation, forms and scripts.
// It stops as soon as limit is reached: the only question is "is there substantial article text?".
// A parse failure reports zero, which keeps the conservative wall decision.
func articleTextChars(body []byte, limit int) int {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return 0
	}
	count := 0
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, skip bool) {
		if count >= limit || n == nil {
			return
		}
		if n.Type == html.ElementNode && wallBoilerplate[n.Data] {
			skip = true
		}
		if n.Type == html.TextNode && !skip {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				count += utf8.RuneCountInString(text)
			}
		}
		for c := n.FirstChild; c != nil && count < limit; c = c.NextSibling {
			walk(c, skip)
		}
	}
	walk(root, false)
	return count
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
