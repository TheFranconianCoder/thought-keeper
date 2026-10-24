package entities

// WallKind names why a fetch did not return the page: a login wall, a paywall, a consent gate. It is
// EVIDENCE, not a guess — a page whose bytes can be read has the zero value, and a heuristic that
// merely suspects something must leave it alone.
//
// The distinction matters because a wall must never become knowledge: a login form or a teaser is
// not what the source says. A walled URL is reported (a red capture light and the reason), and the
// human may attach the page they can see (see AttachURLSnapshot).
type WallKind string

const (
	// WallNone is no wall: the page answered with the page.
	WallNone WallKind = ""
	// WallLogin is a login wall: 401/403, a redirect to the site's sign-in, or a password form.
	WallLogin WallKind = "login_required"
	// WallPaywall is a subscription gate: the article's teaser came back instead of the article.
	WallPaywall WallKind = "paywalled"
	// WallConsent is a consent gate: the fetch was redirected to a cookie/consent host.
	WallConsent WallKind = "consent_required"
)

// Blocks reports whether this is a wall at all.
func (w WallKind) Blocks() bool { return w != WallNone }

// Label is the short name a row carries ("login required").
func (w WallKind) Label() string {
	switch w {
	case WallLogin:
		return "login required"
	case WallPaywall:
		return "paywalled"
	case WallConsent:
		return "consent required"
	default:
		return ""
	}
}

// Reason is the one sentence the source's record carries: what happened, in the reader's words.
func (w WallKind) Reason() string {
	switch w {
	case WallLogin:
		return "the page answered with a login wall"
	case WallPaywall:
		return "the page is behind a paywall"
	case WallConsent:
		return "the page redirected to a consent wall"
	default:
		return ""
	}
}

// Hint tells the reader what can be done about it. It is the same for every wall on purpose: the
// app does not authenticate, so the answer is always the human's browser plus an attached snapshot.
func (w WallKind) Hint() string {
	if !w.Blocks() {
		return ""
	}
	return "Open it in your browser and attach what you can see (save the page as HTML or PDF)."
}
