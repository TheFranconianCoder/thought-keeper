// Package partialentities holds DTOs that use cases receive and return. They carry a
// toEntity method for the adapter layer.
package partialentities

import (
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
)

// PartialSource is the DTO for entities.Source.
type PartialSource struct {
	ID              ulid.ULID
	Hash            string
	Kind            string
	Ext             string
	Path            string
	URL             string
	Title           string
	Language        string
	Origin          string
	WikiPage        string
	ParentHash      string
	Sitemap         bool
	RSS             bool
	Listing         bool
	FromFeed        bool
	FeedHash        string
	Wall            string
	Attached        bool
	AttachedFrom    string
	Bytes           int64
	Pages           int
	TreeState       string
	URLStatus       int
	URLChecked      time.Time
	Bookmark        bool
	ImportedAt      time.Time
	Meta            string
	OriginalName    string
	Tags            []string
	AutoTags        []string
	AutoTagsAt      time.Time
	PublishedAt     time.Time
	PublishedFrom   string
	FavoriteAt      time.Time
	ReadLaterAt     time.Time
	ReadLaterDoneAt time.Time
	ReadLaterManual bool
	ReadLaterVia    []string
	PartsTotal      int
	PartsDone       int
	PartsDerived    int
	AtomsLeft       int
	AtomCount       int
	ProvenanceRows  int
	Steps           entities.StepSet
	// Children is how many sub-sources this source owns (a sitemap's pages, a wiki page's linked
	// URLs). It is filled by the SINGLE-source read only: a list row has no use for it.
	Children int
	// Declared is how many articles a LIST source declares in its snapshot — which is not the same
	// as Children: a URL another owner brought in first stays there. Filled by the single-source
	// read only, and zero for a document.
	Declared int
	// NeedsReset marks a source whose snapshot THIS operation replaced with different text. The
	// caller must rebuild the derivation (exclusive) instead of merely pumping. It is a property of
	// the call, not of the source, and the wire payload ignores it.
	NeedsReset bool
	// SitemapURLs is the expansion a sitemap fetch produced. It is not part of the source entity:
	// the caller claims and fetches these pages with the parent's hash as their owner.
	SitemapURLs []string
	// RSSURLs is the expansion an RSS/Atom feed produced, claimed the same way (origin rss).
	RSSURLs []string
	// ListingURLs is the expansion a listing produced, claimed the same way (origin listing).
	ListingURLs []string
	// ListingItems are the articles the listing's first page declared. The pagination walk appends
	// the later pages and rewrites the listing's snapshot when it is done.
	ListingItems []listing.Item
	// ListingNext is the listing's next page of pagination, or empty when the first page was the
	// whole listing.
	ListingNext string
	// ListingCandidates is how many repeated article cards the detector found on a page that was
	// stored as a normal source. Non-zero means "this may be a listing": the reader is asked once,
	// and a confirmed re-add turns the page into an owner. It is a property of the answer, not of
	// the source, and the wire payload carries it.
	ListingCandidates int
}

// ToEntity converts the partial into a domain entity.
func (p PartialSource) ToEntity() entities.Source {
	return entities.Source{
		ID:              p.ID,
		Hash:            p.Hash,
		Kind:            entities.SourceKind(p.Kind),
		Ext:             p.Ext,
		Path:            p.Path,
		URL:             p.URL,
		Title:           p.Title,
		Bytes:           p.Bytes,
		Pages:           p.Pages,
		TreeState:       entities.TreeState(p.TreeState),
		URLStatus:       p.URLStatus,
		URLChecked:      p.URLChecked,
		Bookmark:        p.Bookmark,
		ImportedAt:      p.ImportedAt,
		Meta:            p.Meta,
		OriginalName:    p.OriginalName,
		Tags:            p.Tags,
		AutoTags:        p.AutoTags,
		AutoTagsAt:      p.AutoTagsAt,
		FavoriteAt:      p.FavoriteAt,
		ReadLaterAt:     p.ReadLaterAt,
		ReadLaterDoneAt: p.ReadLaterDoneAt,
		ReadLaterManual: p.ReadLaterManual,
		ReadLaterVia:    p.ReadLaterVia,
		PublishedAt:     p.PublishedAt,
		PublishedFrom:   p.PublishedFrom,
		Steps:           p.Steps,
		Origin:          p.Origin,
		WikiPage:        p.WikiPage,
		ParentHash:      p.ParentHash,
		Sitemap:         p.Sitemap,
		RSS:             p.RSS,
		Listing:         p.Listing,
		FromFeed:        p.FromFeed,
		FeedHash:        p.FeedHash,
		Wall:            p.Wall,
		Attached:        p.Attached,
		AttachedFrom:    p.AttachedFrom,
	}
}

// PartialFromSource converts a domain entity into its partial.
func PartialFromSource(s entities.Source) PartialSource {
	return PartialSource{
		ID:              s.ID,
		Hash:            s.Hash,
		Kind:            string(s.Kind),
		Ext:             s.Ext,
		Path:            s.Path,
		URL:             s.URL,
		Title:           s.Title,
		Language:        s.Language,
		Origin:          s.Origin,
		WikiPage:        s.WikiPage,
		ParentHash:      s.ParentHash,
		Sitemap:         s.Sitemap,
		RSS:             s.RSS,
		Listing:         s.Listing,
		FromFeed:        s.FromFeed,
		FeedHash:        s.FeedHash,
		Wall:            s.Wall,
		Attached:        s.Attached,
		AttachedFrom:    s.AttachedFrom,
		Bytes:           s.Bytes,
		Pages:           s.Pages,
		TreeState:       string(s.TreeState),
		URLStatus:       s.URLStatus,
		URLChecked:      s.URLChecked,
		Bookmark:        s.Bookmark,
		ImportedAt:      s.ImportedAt,
		Meta:            s.Meta,
		OriginalName:    s.OriginalName,
		Tags:            s.Tags,
		AutoTags:        s.AutoTags,
		AutoTagsAt:      s.AutoTagsAt,
		FavoriteAt:      s.FavoriteAt,
		ReadLaterAt:     s.ReadLaterAt,
		ReadLaterDoneAt: s.ReadLaterDoneAt,
		ReadLaterManual: s.ReadLaterManual,
		ReadLaterVia:    s.ReadLaterVia,
		PublishedAt:     s.PublishedAt,
		PublishedFrom:   s.PublishedFrom,
		PartsTotal:      s.PartsTotal,
		PartsDone:       s.PartsDone,
		PartsDerived:    s.PartsDerived,
		AtomsLeft:       s.AtomsLeft,
		AtomCount:       s.AtomCount,
		ProvenanceRows:  s.ProvenanceRows,
		Steps:           s.Steps,
	}
}
