package entities

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

// The sidecar suffixes: the DERIVED files that belong to one source and lie next to its object. The
// rule (AGENTS.md → Sidecars) is that they carry the source's name plus one of these, so a source is
// one folder entry with its whole story — and deleting the source deletes the story.
//
// They live here, in the core, because "which files does a source have" is domain knowledge: the
// stores that write them (objects, sourcemeta, tree, sourcelog) and the use cases that reason about
// them all read the same list, so the layout cannot drift apart.
const (
	// SidecarRender is the readable rendering of a source's content — what step 1 (capture) leaves
	// behind for a source whose bytes are not readable as they are (a PDF, an EPUB).
	//
	// It is named after the step that writes it, like every other sidecar (.meta, .outline, .log),
	// and it ends in .md because that is what it IS: a Markdown document, readable in any editor. The
	// name is doing real work: <hash>.capture.md is not "64 hex characters plus one extension", so it
	// can never be mistaken for an object — which is exactly what a plain <hash>.md could be, because
	// a Markdown SOURCE's object is <hash>.md too (and a URL snapshot is a third one). Nothing has to
	// guess from the source's extension any more.
	SidecarRender = ".capture.md"
	// SidecarMeta is the step record of a source (steps 1..3).
	SidecarMeta = ".meta"
	// SidecarOutline is the source's node tree (step 2).
	SidecarOutline = ".outline"
	// SidecarSummary is the source's OVERVIEW: prose the agent writes from the claims right after a
	// complete distill. It is derived (a model wrote it, and it can always be written again), it is
	// not a claim, and it is what the Summary tab shows. It lives beside the source like the outline,
	// so a missing summary is a visible file the consistency job can fill in.
	SidecarSummary = ".summary.md"
	// SidecarLog is the source's own log (everything that was done to it).
	SidecarLog = ".log"
)

// SidecarRenderLegacy is the rendering's first name ("<hash>.text.md"). It is not written any more,
// but a vault from before the first rename still carries it — so it is cleaned up with the source and
// never scanned as an object.
const SidecarRenderLegacy = ".text.md"

// SidecarRenderRenames are ALL earlier names of the rendering, the ambiguous plain ".md" included.
// They are what a delete or a re-capture removes. Only the unambiguous ones may be skipped by the
// OBJECT SCAN by name: a plain ".md" is a rendering or a Markdown source depending on what sits
// beside it (objects.parseObject), so it must never be skipped on its name alone.
var SidecarRenderRenames = []string{SidecarRenderLegacy, ".md"}

// SidecarSuffixes lists every derived file of a source, in the order they appear in its life.
func SidecarSuffixes() []string {
	return []string{SidecarRender, SidecarMeta, SidecarOutline, SidecarSummary, SidecarLog}
}

// SourceRecord is what a source's <hash>.meta file holds about its DOCUMENT steps: the state of
// capture, outline and distill, the title, and the record of the PARTS behind the distill step.
//
// The parts belong here for a reason the four lights depend on: everything else in the pipeline can
// be rebuilt from the vault (the claims are files, the outline is a file, the rendering is a file) —
// but "this part was finished" is a fact about a run, and if it only lived in the index, a vault
// whose index was recreated would show green lights over "0 of 161 parts done". The part record is
// therefore a file too.
type SourceRecord struct {
	// Identity is what this source IS, as opposed to what has been derived from it.
	Identity SourceIdentity
	Steps    StepSet
	Title    string
	// Tags are the HUMAN's labels for the source (the source-tags vocabulary observes them). They are
	// a record like the title: deleting the source deletes them, and a reindex reads them back.
	Tags []string
	// AutoTags are the agent's labels. They are kept in the record too (a reindex must not trigger a
	// model call for every source) but they are DERIVED: a full reset clears them and the next
	// complete distill writes new ones.
	AutoTags   []string
	AutoTagsAt time.Time
	Parts      map[string]PartRecord
}

// URLHash is the ADDRESS of a URL source: a stable hash of the URL itself.
//
// A URL's content is a moving target, so it cannot be the identity — the snapshot behind the address
// is what a refresh replaces. The URL is what stays, so the URL is what the address is made of. The
// "url:" prefix is deliberate: without it a file whose bytes happen to be exactly the URL string
// would land on the same address, and two sources would share one row.
func URLHash(url string) string {
	sum := sha256.Sum256([]byte("url:" + strings.TrimSpace(url)))
	return hex.EncodeToString(sum[:])
}

// URLKey is the form in which two URLs are the SAME source. It ignores a trailing slash on the path
// and the fragment: "/article" and "/article/" address one page, and treating them as two produced
// two sources, two fetches and a second derivation of the same article. The ADDRESS of a source stays
// the URL as it was given (URLHash) — only the comparison is normalised.
func URLKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	u.Fragment = ""
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

// SourceIdentity is what a source IS: its original kind, the address it was fetched from, whether it
// is a collected link, and the name it came in under.
//
// It belongs in the source's record because the object's FILE NAME cannot carry it any more. A URL
// snapshot and a Markdown source are both <hash>.md, and a PDF's readable rendering is a third
// <hash>.md — so the extension says "this is Markdown", and only the record says WHAT the source is.
// Without it a reindex (or a fresh index over a copied vault) would turn a URL into a plain Markdown
// source and lose the address it came from; a bookmark would be distilled, which the pipeline
// forbids. The record is read back for exactly that reason.
type SourceIdentity struct {
	Kind       SourceKind
	URL        string
	Bookmark   bool
	Language   string
	Origin     string
	WikiPage   string
	ParentHash string
	Sitemap    bool
	// RSS marks a source that is itself a feed: its snapshot is the item list and its children are
	// the pages the items point at. Like Sitemap it is identity, not derived state.
	RSS          bool
	OriginalName string
	// ContentHash is the SNAPSHOT's hash: what a refresh compares to tell "the same page" from "a
	// changed page".
	//
	// It belongs HERE, in the vault, and that is not a detail. With it only in the index, a reindex —
	// or a deleted index.db, which is the documented repair — left every URL source without one, so
	// the next refresh saw every page as changed and filed a second snapshot for each of them. The
	// index is disposable; "what this snapshot is" is not.
	ContentHash string
	// FaviconHash/FaviconExt locate the site's stored icon. The bytes are derived and disposable, but
	// the reference to them is not: without it a reindex loses the icon of every URL source.
	FaviconHash string
	FaviconExt  string
	// Attached marks a URL snapshot the HUMAN supplied because the app could not read the page (a
	// login, a paywall). It is part of the identity for one reason: the refresh must never replace it
	// with whatever the next fetch answers — a login page is not a correction of the article. The
	// name and hash of the file it came from are kept with it, so the receipt survives a reindex.
	Attached     bool
	AttachedFrom string
	AttachedHash string
	// FromFeed/FeedHash mark a snapshot taken from the FEED's copy of the post (see
	// entities.Source.FromFeed). Part of the identity: a reindex must keep it, or the next refresh
	// would replace the article with the JavaScript shell the feed had solved.
	FromFeed bool
	FeedHash string
}

// PartRecord is one part's distilling outcome.
type PartRecord struct {
	State  PartStateValue `json:"state"`
	Reason string         `json:"reason,omitempty"`
	Atoms  int            `json:"atoms,omitempty"`
	At     time.Time      `json:"at"`
}

// PartsDone counts the parts recorded as finished.
func (r SourceRecord) PartsDone() int {
	n := 0
	for _, p := range r.Parts {
		if p.State == PartDone {
			n++
		}
	}
	return n
}
