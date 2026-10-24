// Package entities holds the domain objects. No external dependencies.
package entities

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// SourceKind is the kind of a raw source.
type SourceKind string

const (
	SourceKindMD  SourceKind = "md"
	SourceKindPDF SourceKind = "pdf"
	SourceKindTXT SourceKind = "txt"
	SourceKindURL SourceKind = "url" // a fetched URL snapshot, stored as Markdown
)

// TreeState tracks the per-source tree index.
type TreeState string

const (
	TreeNone    TreeState = "none"
	TreePending TreeState = "pending"
	TreeBuilt   TreeState = "built"
	TreeFailed  TreeState = "failed"
)

// AllTags is the human labels plus the derived ones, deduplicated: what a reader filters by and what
// the source-tags vocabulary observes.
func (s Source) AllTags() []string {
	seen := make(map[string]bool, len(s.Tags)+len(s.AutoTags))
	out := make([]string, 0, len(s.Tags)+len(s.AutoTags))
	for _, list := range [][]string{s.Tags, s.AutoTags} {
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

// SourceTagSet is one source's human and derived labels as the vocabulary reads them.
type SourceTagSet struct {
	Tags       []string
	AutoTags   []string
	AutoTagsAt time.Time
}

// NeedsCapture reports whether a source's BYTES are not readable as they are, so step 1 has real
// work to do: a PDF's text has to be extracted (poppler, OCR). Markdown, text and a URL snapshot are
// already the readable form — their capture is the intake itself.
func (k SourceKind) NeedsCapture() bool { return k == SourceKindPDF }

// Source is one immutable raw object in the content-addressed store.
type Source struct {
	ID    ulid.ULID
	Hash  string // sha256 hex = the content address
	Kind  SourceKind
	Ext   string // ".md" | ".pdf" | ".txt"
	Path  string // vault-relative sharded path
	URL   string // origin, for kind=url
	Title string
	// Language is the source's detected language as an ISO 639-1 code (or ISO 639-3 when no
	// 639-1 exists). It is a HINT for search, never a filter, and it lives in the source record
	// (<hash>.meta) so a reindex restores it.
	Language string
	// Origin/WikiPage mark a source created by a wiki page. They are a hint for removal: a URL
	// that was independently collected must not disappear just because a page stopped linking it.
	// ParentHash is the owning source for a sitemap page (Origin=sitemap); a dependent source has
	// exactly one owner and cannot be deleted on its own.
	Origin     string
	WikiPage   string
	ParentHash string
	// Sitemap marks a URL source that is itself a sitemap: its snapshot is the page list and its
	// children are the sources it brought into the vault.
	Sitemap bool
	// RSS marks a URL source that is itself an RSS/Atom feed: its snapshot is the item list and its
	// children are the pages its items point at. A feed is not a complete list of a site: a child
	// that later disappears from it is kept.
	RSS bool
	// FromFeed marks a URL snapshot whose bytes came from the FEED's own copy of the post (many feeds
	// carry the whole article while the page is a JavaScript shell). FeedHash names that feed. A
	// readable page may replace the copy; while it cannot, the copy is what the vault knows.
	FromFeed bool
	FeedHash string
	// OriginalName is the raw input's own name (the dropped file's name, or the URL): shown
	// *additionally*, because the title belongs to the document and is chosen from its content.
	OriginalName string
	Bytes        int64
	Pages        int
	TreeState    TreeState
	URLStatus    int       // last HTTP status for kind=url (0 = unknown / fetch failed)
	URLChecked   time.Time // last time a URL source was checked
	Bookmark     bool      // true = collected link, not indexed (no tree, no atoms)
	// Tags are the HUMAN's labels for this source — the same coarse words a wiki page carries, so a
	// reader can file and filter sources. They live in the source's <hash>.meta record (the truth) and
	// are mirrored into the index.
	Tags []string
	// AutoTags are the labels the agent derives from the source's claims, kept apart from the human's
	// (a derived tag never re-adds one that was deleted). AutoTagsAt is when the derivation last ran;
	// zero means this source was never tagged and the consistency job owes it a pass.
	AutoTags   []string
	AutoTagsAt time.Time
	ImportedAt time.Time
	Meta       string // free-form JSON
	AtomCount  int    // derived: atoms that cite this source and can be resolved
	// ProvenanceRows is the raw number of atom_sources rows, orphans included. It differs from
	// AtomCount exactly when the index carries damaged rows — which is a fact worth showing.
	ProvenanceRows int

	// Progress of the derivation: how many parts exist (one text-bearing node each), how many are
	// finished, and how many claims still wait for a page. This is what "what is left to do" means
	// for a source.
	PartsTotal int
	PartsDone  int
	// PartsDerived counts the finished parts that actually derived content. A source_relative
	// part (book metadata, navigation) is correctly done without a claim; it must not make the
	// distill step look started when no real part has run yet.
	PartsDerived int
	AtomsLeft    int

	// Steps is the state of this source's DOCUMENT steps (capture, outline, distill). It is a
	// mirror of the vault's <hash>.meta file, which is the truth: reindex rebuilds this field
	// from the file, never the other way round.
	Steps StepSet
}

// hashOf is the domain-separated hash used by stable source addresses (a URL, a wiki page).
func hashOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Object is a raw object as seen by the content-addressed store.
type Object struct {
	Hash  string
	Ext   string
	Path  string
	Bytes int64
}

// KindForExt maps a file extension to a source kind, or "" when unsupported.
func KindForExt(ext string) SourceKind {
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return SourceKindMD
	case ".pdf":
		return SourceKindPDF
	case ".txt":
		return SourceKindTXT
	default:
		return ""
	}
}
