package payloads

import "github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"

// WikiSummary is one page in the wiki list.
type WikiSummary struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
	// AutoTags are the tags the app derived from the page; the human's tags are not repeated here.
	AutoTags   []string `json:"auto_tags,omitempty"`
	SourceHash string   `json:"source_hash,omitempty"`
	// FavoriteAt is the reader's star on the page (the frontmatter is the truth).
	FavoriteAt string `json:"favorite_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// WikiTag is one tag in the cloud.
type WikiTag struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// WikiList is GET /api/v1/wiki.
type WikiList struct {
	Pages []WikiSummary `json:"pages"`
	Tags  []WikiTag     `json:"tags"`
	Count int           `json:"count"`
}

// WikiPage is one page, including its Markdown body.
type WikiPage struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
	// AutoTags were derived from the page's own title and prose. They are shown beside the human's
	// tags, marked as derived — and deleting one is respected by the next tagging run.
	AutoTags []string `json:"auto_tags,omitempty"`
	// URLs are the external sources tracked by this page.
	URLs       []string `json:"urls,omitempty"`
	Body       string   `json:"body"`
	SourceHash string   `json:"source_hash,omitempty"`
	FavoriteAt string   `json:"favorite_at,omitempty"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// WikiSaveRequest is the body of POST /wiki and PUT /wiki/{id}.
type WikiSaveRequest struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
	Body  string   `json:"body"`
}

func WikiListFromUseCase(in usecases.WikiList) WikiList {
	out := WikiList{Pages: make([]WikiSummary, 0, len(in.Pages)), Tags: make([]WikiTag, 0, len(in.Tags))}
	for _, p := range in.Pages {
		out.Pages = append(out.Pages, WikiSummary{
			ID: p.ID.String(), Title: p.Title, Tags: p.Tags, AutoTags: p.AutoTags,
			SourceHash: p.SourceHash, FavoriteAt: wireTime(p.FavoriteAt),
			UpdatedAt: p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	for _, t := range in.Tags {
		out.Tags = append(out.Tags, WikiTag{Tag: t.Tag, Count: t.Count})
	}
	out.Count = len(out.Pages)
	return out
}
