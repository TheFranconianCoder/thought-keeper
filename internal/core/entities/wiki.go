package entities

import (
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// WikiPrefix is the address prefix of a WHOLE WIKI PAGE. A page is user-authored Markdown with a
// stable identity; it is also stored as a source, but that source is derived from the page and may
// be replaced in place. The prefix is what tells the two apart in a source record.
const WikiPrefix = "wiki:"

// AllTags is the page's tags as one set: what a reader filters by, and what the tag vocabulary
// observes. The human's label wins when the same tag is both written and derived.
func (p WikiPage) AllTags() []string {
	seen := make(map[string]bool, len(p.Tags)+len(p.AutoTags))
	out := make([]string, 0, len(p.Tags)+len(p.AutoTags))
	for _, list := range [][]string{p.Tags, p.AutoTags} {
		for _, tag := range list {
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

// WikiLinkOrigin is the Origin of a URL source that a wiki page brought into the vault. Removal uses
// it: a link that leaves a page may only take a source with it that a page created, never one that
// was collected independently.
const WikiLinkOrigin = "wiki"

// SitemapOrigin is the Origin of a URL source that a sitemap brought into the vault. Its owner is
// the sitemap source named by ParentHash; removal of that source removes it too.
const SitemapOrigin = "sitemap"

// RSSOrigin is the Origin of a URL source that an RSS/Atom feed brought into the vault. Its owner is
// the feed source named by ParentHash; removal of the feed removes it too. Unlike a sitemap, a page
// that DISAPPEARS from the feed is kept: a feed is a window on the newest items, not a complete list.
const RSSOrigin = "rss"

// OwnsChildren reports whether a source is an OWNER: a sitemap or a feed owns the URL sources it
// declared, and deleting it removes the whole subtree.
func (s Source) OwnsChildren() bool { return s.Sitemap || s.RSS }

// ChildOrigin is the Origin its children carry.
func (s Source) ChildOrigin() string {
	switch {
	case s.Sitemap:
		return SitemapOrigin
	case s.RSS:
		return RSSOrigin
	default:
		return ""
	}
}

// WikiPage is one user-authored wiki page. The page is the truth: its Markdown file lives in the
// vault and carries the identity, the tags and the link to the source representation.
//
// A page is NOT a cluster and NOT a claim. Clusters are titleless associations of claims; a wiki page
// has a title and prose because a human wrote it. Its claims are derived from the source
// representation, so the wiki and the Zettelkasten use the same pipeline without sharing a table.
type WikiPage struct {
	ID    ulid.ULID
	Title string
	// Tags are the HUMAN's labels. They live in the frontmatter and are edited in the page form.
	Tags []string
	// AutoTags are the tags the app DERIVED from the page (title and prose). They are kept apart from
	// the human's tags so a tagging run never re-adds a tag the reader deleted, and so the UI can say
	// which labels it invented. Both kinds feed the same tag vocabulary.
	AutoTags []string
	Body     string
	// URLs are the external http(s) sources declared in the page body. They are tracked in the
	// page frontmatter so a save can add/remove exactly the delta without re-fetching the rest.
	URLs []string
	// SourceHash is the address of the CURRENT source representation of this page. It is stable for
	// the page's lifetime (WikiHash), so an edit re-processes the same source from capture instead of
	// filing a second one beside it.
	SourceHash string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WikiHash is the stable address of a wiki page's source representation, the way URLHash is the
// stable address of a URL snapshot. The page can change; its address must not.
func WikiHash(id ulid.ULID) string {
	return hashOf("wiki:" + id.String())
}

// IsWikiOriginalName reports whether a source record names a wiki page. It is deliberately stored in
// OriginalName: the source's kind stays Markdown (the extractor and every reader already know it),
// while this prefix says that the bytes are owned by a wiki page and may be rewritten in place.
func IsWikiOriginalName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), WikiPrefix)
}

// WikiIDFromOriginalName returns the page id encoded in a source's original name, or "".
func WikiIDFromOriginalName(name string) string {
	name = strings.TrimSpace(name)
	if !IsWikiOriginalName(name) {
		return ""
	}
	return strings.TrimPrefix(name, WikiPrefix)
}

// IsWiki reports whether this source is the representation of a wiki page.
func (s Source) IsWiki() bool { return IsWikiOriginalName(s.OriginalName) }

// NeedsCapture reports whether step 1 must produce a readable rendering. A wiki page is stored as
// Markdown but still goes through capture: an edit must start at step 1, and the rendering is what
// the outline reads, so a page and a dropped Markdown file follow exactly the same contract.
func (s Source) NeedsCapture() bool { return s.Kind.NeedsCapture() || s.IsWiki() }
