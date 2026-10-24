// Package partialentities holds DTOs that use cases receive and return. They carry a
// toEntity method for the adapter layer.
package partialentities

import (
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// PartialSource is the DTO for entities.Source.
type PartialSource struct {
	ID             ulid.ULID
	Hash           string
	Kind           string
	Ext            string
	Path           string
	URL            string
	Title          string
	Language       string
	Origin         string
	WikiPage       string
	ParentHash     string
	Sitemap        bool
	RSS            bool
	FromFeed       bool
	FeedHash       string
	Bytes          int64
	Pages          int
	TreeState      string
	URLStatus      int
	URLChecked     time.Time
	Bookmark       bool
	ImportedAt     time.Time
	Meta           string
	OriginalName   string
	Tags           []string
	AutoTags       []string
	AutoTagsAt     time.Time
	PartsTotal     int
	PartsDone      int
	PartsDerived   int
	AtomsLeft      int
	AtomCount      int
	ProvenanceRows int
	Steps          entities.StepSet
	// Children is how many sub-sources this source owns (a sitemap's pages, a wiki page's linked
	// URLs). It is filled by the SINGLE-source read only: a list row has no use for it.
	Children int
	// SitemapURLs is the expansion a sitemap fetch produced. It is not part of the source entity:
	// the caller claims and fetches these pages with the parent's hash as their owner.
	SitemapURLs []string
	// RSSURLs is the expansion an RSS/Atom feed produced, claimed the same way (origin rss).
	RSSURLs []string
}

// ToEntity converts the partial into a domain entity.
func (p PartialSource) ToEntity() entities.Source {
	return entities.Source{
		ID:           p.ID,
		Hash:         p.Hash,
		Kind:         entities.SourceKind(p.Kind),
		Ext:          p.Ext,
		Path:         p.Path,
		URL:          p.URL,
		Title:        p.Title,
		Bytes:        p.Bytes,
		Pages:        p.Pages,
		TreeState:    entities.TreeState(p.TreeState),
		URLStatus:    p.URLStatus,
		URLChecked:   p.URLChecked,
		Bookmark:     p.Bookmark,
		ImportedAt:   p.ImportedAt,
		Meta:         p.Meta,
		OriginalName: p.OriginalName,
		Tags:         p.Tags,
		AutoTags:     p.AutoTags,
		AutoTagsAt:   p.AutoTagsAt,
		Steps:        p.Steps,
		Origin:       p.Origin,
		WikiPage:     p.WikiPage,
		ParentHash:   p.ParentHash,
		Sitemap:      p.Sitemap,
		RSS:          p.RSS,
		FromFeed:     p.FromFeed,
		FeedHash:     p.FeedHash,
	}
}

// PartialFromSource converts a domain entity into its partial.
func PartialFromSource(s entities.Source) PartialSource {
	return PartialSource{
		ID:             s.ID,
		Hash:           s.Hash,
		Kind:           string(s.Kind),
		Ext:            s.Ext,
		Path:           s.Path,
		URL:            s.URL,
		Title:          s.Title,
		Language:       s.Language,
		Origin:         s.Origin,
		WikiPage:       s.WikiPage,
		ParentHash:     s.ParentHash,
		Sitemap:        s.Sitemap,
		RSS:            s.RSS,
		FromFeed:       s.FromFeed,
		FeedHash:       s.FeedHash,
		Bytes:          s.Bytes,
		Pages:          s.Pages,
		TreeState:      string(s.TreeState),
		URLStatus:      s.URLStatus,
		URLChecked:     s.URLChecked,
		Bookmark:       s.Bookmark,
		ImportedAt:     s.ImportedAt,
		Meta:           s.Meta,
		OriginalName:   s.OriginalName,
		Tags:           s.Tags,
		AutoTags:       s.AutoTags,
		AutoTagsAt:     s.AutoTagsAt,
		PartsTotal:     s.PartsTotal,
		PartsDone:      s.PartsDone,
		PartsDerived:   s.PartsDerived,
		AtomsLeft:      s.AtomsLeft,
		AtomCount:      s.AtomCount,
		ProvenanceRows: s.ProvenanceRows,
		Steps:          s.Steps,
	}
}
