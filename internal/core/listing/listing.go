// Package listing decides whether a page is a LIST of articles and which links are the articles.
//
// It is pure: the HTML is parsed by the link extractor in the infrastructure layer, which reduces
// every anchor to a Link. Everything here — the filters, the repeated-block grouping and the
// thresholds — is a rule about links, so it can be tested without a browser, a network or a model.
//
// The rules were calibrated on real pages, and the differences between them are why the package
// looks the way it does:
//
//   - a WordPress blog index (agilealliance.org/blog) renders 20 article cards whose titles are
//     <h3> links, next to a navigation with 16 links that are NOT in headings. The readable text of
//     that page is 221 characters — readability finds no article, so the page would become a
//     bookmark.
//   - a beehiiv archive (sandeepswadia.beehiiv.com/archive) renders 9 cards whose anchors carry a
//     date and no heading at all. Readability DOES read the grid there (966 characters, 9 links).
//   - a Webflow blog index (langchain.com/blog) renders 20 cards as OVERLAY links: the anchor is
//     empty and the title and date sit beside it in the card (`<a class="…-absolute"></a>`). Zero
//     candidates, unless the card around the link lends it a name.
//   - a JavaScript landing page (thoughtworks.com/…/insights/blog) answers with its MEGA-MENU and
//     nothing else. Ten navigation tiles with headings looked exactly like ten articles.
//   - a Genesis blog index (fs.blog/blog) renders 16 cards with heading links and words its pager
//     "See older articles" — a verb in front of the words. The next page is declared as
//     `<link rel="next">` in the head and nowhere else an anchor could carry it.
//
// So neither "the page has many links" nor "the page has little text" alone is a signal. A group of
// repeated, article-shaped links is the signal; the readable text decides whether that group is the
// page (the text is the list) or a rail beside an article (the text is the article) — and navigation
// is never a card.
package listing

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// Link is one anchor of a page, reduced to what a listing decides on. The extractor fills it; the
// rules below never look at the document again.
type Link struct {
	// URL is the resolved, absolute address.
	URL string
	// Text is the anchor's visible text, whitespace-normalised.
	Text string
	// Label is the anchor's own name when it declares one (aria-label, title). It is the cleanest
	// title a card offers: many cards concatenate a date, a title and an excerpt into their text.
	Label string
	// InHeading is true when the anchor sits inside h1–h6.
	InHeading bool
	// Card is true for an OVERLAY link: an anchor that carries no text of its own because the block
	// around it draws the card, and that block renders the title as a heading (a Webflow
	// "absolute link" over the whole tile). It is the heading evidence, borrowed from the card.
	Card bool
	// InNav is true when the anchor sits inside the page's navigation: <nav>, <header>, <footer> or
	// an element that declares role="navigation|menu|menubar|search" (or is aria-hidden). A menu
	// renders as repeated tiles with headings, and a thin page would read as a list of its own menu.
	InNav bool
	// Time is the text of a <time> element inside the anchor (or its datetime attribute), when the
	// card declares a date that way instead of writing it into the text.
	Time string
	// Group is the structural signature of the anchor's ancestors: the last few tag names only
	// ("div/div/h3"). Tag names, never classes — a class that carries an id (`post-8115405`,
	// `elementor-repeater-item-a6c565a`) is unique per card and would split every group.
	Group string
	// Rel is the anchor's rel attribute, lower-cased ("next" for a pager).
	Rel string
}

// Item is one article a listing declares: the address, the best title the card offers and whether
// the card carried a date.
type Item struct {
	URL   string
	Title string
	// Date is the card's date as it was written ("Oct 27, 2026"), or empty.
	Date string
}

// Result is what the rules found on one page.
type Result struct {
	// Items are the article links, in document order, deduplicated.
	Items []Item
	// GroupSize is how many links the chosen block had BEFORE deduplication. It is the confidence:
	// 20 repeated cards are a listing, 3 links are a "related posts" rail.
	GroupSize int
	// Headings and Dates are how many of the chosen block's links carried a heading or a date. A
	// block qualifies as article-shaped through one of them.
	Headings int
	Dates    int
	// Next is the address of the listing's next page, or empty.
	Next string
	// Analyzed is how many anchors the page offered at all. It is a diagnostic, not a signal: it is
	// what tells "the page has no repeated block" from "the page has no links".
	Analyzed int
}

// Detect runs the rules over one page and returns the articles it found.
//
// pageURL is the address the page was fetched from; links are already absolute. The returned Result
// is a SUGGESTION — Suggests turns it into an answer, with the readable text the converter measured.
// Nothing here reclassifies a page on its own: the caller offers the listing to the human, because a
// page misread as a list is never distilled.
func Detect(pageURL string, links []Link) Result {
	res := Result{Next: NextPage(pageURL, links), Analyzed: len(links)}
	group := articleGroup(pageURL, links)
	if len(group) == 0 {
		return res
	}
	res.GroupSize = len(group)
	seen := make(map[string]bool, len(group))
	for _, link := range group {
		key := entities.URLKey(link.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		res.Items = append(res.Items, Item{URL: link.URL, Title: titleOf(link), Date: dateOf(link)})
		if link.InHeading || link.Card {
			res.Headings++
		}
		if dateOf(link) != "" {
			res.Dates++
		}
	}
	return res
}

// Suggests reports whether a Result is a listing worth offering to the human.
//
// Two rules, and both are needed:
//
//   - at least MinArticles repeated, article-shaped links (a "related posts" rail of three is not a
//     listing);
//   - the readable text is short (readability found no article — the page IS the list) or the links
//     are IN that readable text (the list survived extraction, so it is the content).
//
// A long article with a related-posts rail fails both: its text is long and the rail is not in it.
func Suggests(res Result, mainTextChars, minTextChars, mainURLs int) bool {
	if len(res.Items) < MinArticles {
		return false
	}
	if minTextChars <= 0 {
		minTextChars = DefaultMinTextChars
	}
	return mainTextChars < minTextChars || mainURLs >= MinArticles
}

// MinArticles is the smallest number of repeated cards that counts as a listing. Three is the size
// of a "related posts" rail; five leaves room for a fourth.
const MinArticles = 5

// DefaultMinTextChars mirrors the app's default bookmark threshold. It is only a fallback: the
// caller passes its own configured value.
const DefaultMinTextChars = 400

// PageItems extracts the articles of one listing page without judging whether the page is one. It
// is what a refresh and a pagination walk use: the page is already known to be a listing.
func PageItems(pageURL string, links []Link) []Item {
	return Detect(pageURL, links).Items
}

// NextPage returns the listing's next page: rel="next" first (the standard the big sites set), then
// a "load more"/"older"/"weiter" anchor whose address is a pager. It never returns the page itself.
func NextPage(pageURL string, links []Link) string {
	self := entities.URLKey(pageURL)
	for _, link := range links {
		if strings.Contains(link.Rel, "next") && entities.URLKey(link.URL) != self && isHTTP(link.URL) {
			return link.URL
		}
	}
	for _, link := range links {
		if !isHTTP(link.URL) || entities.URLKey(link.URL) == self {
			continue
		}
		if !pagerText(link) || !pagerURL(pageURL, link.URL) {
			continue
		}
		return link.URL
	}
	return ""
}

// pagerText is the small vocabulary a "next page" anchor uses. Short by nature: an article title is
// never "load more". The LABEL is read as well: a pager is often an arrow icon whose words live in
// aria-label ("Next Page"), and an icon carries no text at all.
func pagerText(link Link) bool {
	return isPagerPhrase(link.Text) || isPagerPhrase(link.Label)
}

// isPagerPhrase answers the vocabulary question for one piece of text. A site may open its pager
// with a verb ("See older articles" on fs.blog): the verb is what the site chose, not what the link
// does, so a leading one is dropped before the words are looked up. That is safe because the words
// alone never decide anything — the address still has to be a pager (pagerURL) before NextPage takes
// the link and before usable drops it. It is also why "Read more" ("read" + "more") is not a pager
// when it points at an article.
func isPagerPhrase(text string) bool {
	text = trimPager(text)
	if lead, rest, ok := strings.Cut(text, " "); ok && pagerLead[lead] {
		text = trimPager(rest)
	}
	switch text {
	case "load more", "load more posts", "load more articles", "show more", "show more posts",
		"next", "next page", "next posts", "older", "older posts", "older entries", "older articles",
		"previous", "previous page", "newer", "newer posts", "newer articles", "more", "more posts",
		"more articles", "mehr", "mehr laden", "weitere", "weitere beiträge",
		"nächste", "nächste seite", "ältere", "ältere beiträge", "vorherige", "neuere":
		return true
	}
	return false
}

// trimPager normalises a pager label: lower case, single spaces, no leading or trailing punctuation
// or arrows ("See older articles »").
func trimPager(text string) string {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.TrimFunc(text, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}

// pagerLead is the verb a site may put in front of its pager words.
var pagerLead = map[string]bool{"see": true, "view": true, "show": true, "read": true, "browse": true}

// pagerURL reports whether the candidate address looks like another page of the same listing: the
// same path with a page query (`?page=2`, `?paged=2`, `?offset=10`), or a `/page/N` suffix.
func pagerURL(pageURL, candidate string) bool {
	base, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	next, err := url.Parse(candidate)
	if err != nil || !sameHost(base, next) {
		return false
	}
	for key, values := range next.Query() {
		if len(values) == 0 || !isNumber(strings.TrimSpace(values[0])) {
			continue
		}
		if pagerKey(key) {
			return true
		}
	}
	return pageSegment(next.Path)
}

// pagerKey names the query keys that carry a page number: the standard ones, and the namespaced form
// a CMS writes when several lists share one address (`?8457a1db_page=2` on a Webflow pager).
func pagerKey(key string) bool {
	key = strings.ToLower(key)
	switch key {
	case "page", "paged", "offset", "start":
		return true
	}
	return strings.HasSuffix(key, "_page") || strings.HasSuffix(key, "_paged")
}

// pageSegment reports whether a path ends in "/page/2" (or "/2" after a "page" segment).
func pageSegment(path string) bool {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return false
	}
	if parts[len(parts)-2] != "page" {
		return false
	}
	return isNumber(parts[len(parts)-1])
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// articleGroup picks the repeated block that looks like article cards.
//
// A group qualifies through a heading (the title is a heading — the shape a CMS gives a card), a
// date (the card carries one) or a shared path prefix of at least one segment (`/p/<slug>`). It is
// those three that separate a card grid from a navigation: nav links have no headings, no dates and
// no shared prefix, and their text is short anyway.
func articleGroup(pageURL string, links []Link) []Link {
	groups := make(map[string][]Link)
	order := make([]string, 0, 8)
	for _, link := range links {
		if !usable(pageURL, link) {
			continue
		}
		if link.Group == "" {
			continue
		}
		if _, ok := groups[link.Group]; !ok {
			order = append(order, link.Group)
		}
		groups[link.Group] = append(groups[link.Group], link)
	}

	var best []Link
	for _, key := range order {
		group := groups[key]
		if len(group) < 3 || !articleish(group) {
			continue
		}
		if len(group) > len(best) {
			best = group
		}
	}
	return best
}

// articleish reports whether a group is shaped like article cards rather than like navigation.
func articleish(group []Link) bool {
	headings, dates, samePrefix := 0, 0, 0
	prefix := ""
	for _, link := range group {
		if link.InHeading || link.Card {
			headings++
		}
		if dateOf(link) != "" {
			dates++
		}
		if segment := firstSegment(link.URL); segment != "" {
			switch {
			case prefix == "":
				prefix = segment
				samePrefix = 1
			case segment == prefix:
				samePrefix++
			}
		}
	}
	n := len(group)
	// A majority is the evidence, not unanimity: one link to another section in the same block
	// (a "all posts" tile beside the cards) must not turn 20 cards into "not a block".
	if headings*10 >= n*6 || dates*10 >= n*6 {
		return true
	}
	return samePrefix != 0 && samePrefix*10 >= n*6
}

// usable applies the per-link filter: a link on the same host, not the page itself, with enough
// text or a heading, and never part of the page's navigation. Text length is what lets a menu
// through, so the group rule above does the rest.
func usable(pageURL string, link Link) bool {
	if !isHTTP(link.URL) {
		return false
	}
	// A menu, a header and a footer are the page's furniture: repeated tiles on them are not
	// articles, whatever they look like.
	if link.InNav {
		return false
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	ref, err := url.Parse(link.URL)
	if err != nil || !sameHost(base, ref) {
		return false
	}
	if entities.URLKey(link.URL) == entities.URLKey(pageURL) {
		return false
	}
	// A pager is its WORDS and its ADDRESS — the same rule NextPage applies. The words alone are not
	// enough: "Read more" is the most common card label there is, and the verb-stripping that reads
	// "See older articles" as a pager also reads "Read more" as one ("read" + "more"). A card's
	// "Read more" points at its article, so it is the article's link; a pager points at another page
	// of the list, so it never is.
	if (pagerText(link) && pagerURL(pageURL, link.URL)) || strings.Contains(link.Rel, "next") {
		return false
	}
	if link.InHeading || link.Card || strings.TrimSpace(link.Label) != "" {
		return true
	}
	return utf8.RuneCountInString(strings.TrimSpace(link.Text)) >= 25
}

func isHTTP(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func sameHost(base, ref *url.URL) bool {
	return strings.TrimPrefix(strings.ToLower(base.Host), "www.") ==
		strings.TrimPrefix(strings.ToLower(ref.Host), "www.")
}

// firstSegment is the first path segment of an address ("p" for /p/slug), or empty at the root.
func firstSegment(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	trimmed := strings.Trim(parsed.Path, "/")
	if trimmed == "" {
		return ""
	}
	if before, _, ok := strings.Cut(trimmed, "/"); ok {
		return before
	}
	return trimmed
}

// titleOf picks the cleanest name a card offers: its own label, then its heading, then the text with
// a leading date removed. The page's own title replaces it after the fetch; this is what the reader
// sees in the meantime.
func titleOf(link Link) string {
	title := strings.TrimSpace(link.Label)
	if title == "" {
		title = stripDate(link.Text)
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return link.URL
	}
	return truncate(title, 120)
}

// dateOf returns the card's date as written, or empty. A date is evidence that the block is a feed
// of articles: navigation does not date its links.
func dateOf(link Link) string {
	if value := strings.TrimSpace(link.Time); value != "" {
		return value
	}
	if match := datePattern.FindString(link.Text); match != "" {
		return strings.TrimSpace(match)
	}
	return ""
}

func stripDate(text string) string {
	text = strings.TrimSpace(text)
	if loc := datePattern.FindStringIndex(text); loc != nil && loc[0] == 0 {
		text = strings.TrimSpace(text[loc[1]:])
	}
	return strings.TrimLeft(text, "·-–—|:,")
}

// datePattern recognises the date forms a listing writes into a card: an English or German month
// name with a day and a year, an ISO date, or a dotted date.
var datePattern = regexp.MustCompile(`(?i)` +
	`\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?\s+\d{1,2},?\s+\d{4}` +
	`|\b\d{1,2}\.?\s+(?:januar|februar|märz|april|mai|juni|juli|august|september|oktober|november|dezember)\s+\d{4}` +
	`|\b\d{4}-\d{2}-\d{2}` +
	`|\b\d{1,2}\.\d{1,2}\.\d{4}`)

func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	cut := string(runes[:limit])
	if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}
