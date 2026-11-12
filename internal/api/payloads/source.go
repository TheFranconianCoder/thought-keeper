package payloads

import "github.com/TheFranconianCoder/thought-keeper/internal/core/entities"

// Source is the wire shape of a raw source.
type Source struct {
	Hash  string `json:"hash"`
	Kind  string `json:"kind"`
	Ext   string `json:"ext"`
	Path  string `json:"path"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title"`
	// Language is the detected ISO code (a hint for search, never a filter).
	Language   string `json:"language,omitempty"`
	Origin     string `json:"origin,omitempty"`
	WikiPage   string `json:"wiki_page,omitempty"`
	ParentHash string `json:"parent_hash,omitempty"`
	Sitemap    bool   `json:"sitemap,omitempty"`
	// RSS marks a feed source: its snapshot is the item list and its children are the pages the items
	// point at. A feed only ever grows; a sitemap is reconciled 1:1.
	RSS bool `json:"rss,omitempty"`
	// Listing marks a listing source: a blog index or a newsletter archive whose cards are articles.
	// Like a feed it only grows; its pagination is walked once, on capture.
	Listing bool `json:"listing,omitempty"`
	// ListingCandidates is how many repeated article cards the detector found on a page that was
	// stored as a normal source. It asks the reader one question; it is not a property of the source.
	ListingCandidates int `json:"listing_candidates,omitempty"`
	// FromFeed marks a snapshot taken from a feed's own copy of the post (the page itself may be a
	// JavaScript shell); FeedHash is the feed it came from.
	FromFeed bool   `json:"from_feed,omitempty"`
	FeedHash string `json:"feed_hash,omitempty"`
	// Wall is the last wall the URL answered with (login_required, paywalled, consent_required).
	// Readable bytes can survive a later wall; the reader offers a manual Refresh for the source.
	Wall string `json:"wall,omitempty"`
	// Attached marks a snapshot the human supplied (a login, a paywall, a JavaScript page). The
	// source page offers "replace snapshot" and "try the URL again" for it; the automatic refresh
	// still leaves it alone.
	Attached     bool   `json:"attached,omitempty"`
	AttachedFrom string `json:"attached_from,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	// Tags are the human's labels for this source; AutoTags are the agent's derived ones. Both are
	// observed by the source-tags vocabulary; auto_tags_at marks a tagged source whose answer was
	// empty, so the backfill does not ask again.
	Tags       []string `json:"tags,omitempty"`
	AutoTags   []string `json:"auto_tags,omitempty"`
	AutoTagsAt string   `json:"auto_tags_at,omitempty"`
	// The reader's marks. FavoriteAt is the star. ReadLaterAt/ReadLaterDoneAt are the queue entry;
	// ReadLater is the derived "open" flag the row uses. ReadLaterVia names the interests that
	// queued it, which is how the row explains itself (and why "not this interest" can undo it).
	FavoriteAt      string   `json:"favorite_at,omitempty"`
	ReadLaterAt     string   `json:"read_later_at,omitempty"`
	ReadLaterDoneAt string   `json:"read_later_done_at,omitempty"`
	ReadLaterManual bool     `json:"read_later_manual,omitempty"`
	ReadLaterVia    []string `json:"read_later_via,omitempty"`
	ReadLater       bool     `json:"read_later,omitempty"`
	// PublishedAt is the date the source declares for ITSELF: a page's own publication, a feed
	// item's pubDate, or a wiki page's last edit (never the arrival time). PublishedFrom names the
	// layer that found it: feed, meta, time, text or wiki. Empty means no date is known.
	PublishedAt    string `json:"published_at,omitempty"`
	PublishedFrom  string `json:"published_from,omitempty"`
	PartsTotal     int    `json:"parts_total"`
	PartsDone      int    `json:"parts_done"`
	AtomsLeft      int    `json:"atoms_left"`
	Bytes          int64  `json:"bytes"`
	Pages          int    `json:"pages,omitempty"`
	TreeState      string `json:"tree_state"`
	AtomCount      int    `json:"atom_count"`
	ProvenanceRows int    `json:"provenance_rows"`
	// Children is how many sub-sources this source owns (a sitemap's pages, a wiki page's linked
	// URLs). Zero on a list row; only the detail read fills it.
	Children int `json:"children,omitempty"`
	// Declared is how many articles a list source declares (a sitemap's pages, a feed's items, a
	// listing's articles). It is larger than Children when another owner brought some of them in.
	Declared int `json:"declared,omitempty"`
	// RunningStep names the document step that is running for this source right now
	// ("capture"/"outline"/"distill"), or is empty. The CLUSTER step is not per source (it is reported
	// globally): a source row must not claim to be compiling.
	RunningStep  string `json:"running_step,omitempty"`
	URLStatus    int    `json:"url_status,omitempty"`
	URLCheckedAt string `json:"url_checked_at,omitempty"`
	// URLChangedAt is when the stored snapshot's readable content last actually changed. It is NOT
	// the last check: a page that answered the same has a check time and no change time. The list
	// orders by activity (the change when there is one, otherwise the arrival) and the "changed"
	// filter selects these.
	URLChangedAt string `json:"url_changed_at,omitempty"`
	Bookmark     bool   `json:"bookmark,omitempty"`
	ImportedAt   string `json:"imported_at"`
	// Steps is the state of the four pipeline steps for this source — the four lights. Every step
	// is always present (pending at the very least), so the UI never has to guess a gap.
	Steps map[string]StepStatus `json:"steps"`
	// Sidecars is the source's whole story as files: the object and its derived files, with sizes.
	// A missing file is a size of -1 — exactly what the consistency job repairs. Only the single
	// source read carries it (the list does not stat five files per row).
	Sidecars []Sidecar `json:"sidecars,omitempty"`
}

// Sidecar is one file of a source (the object or one of its derived files).
type Sidecar struct {
	// Name is the suffix (".pdf", ".md", ".meta", ".outline", ".log").
	Name string `json:"name"`
	// Bytes is the file's size, or -1 when it is missing.
	Bytes int64 `json:"bytes"`
	// Object marks the source's own file (the bytes that were dropped in).
	Object bool `json:"object,omitempty"`
}

// StepStatus is one step's state for one source: the traffic light.
type StepStatus struct {
	State  string `json:"state"` // pending | running | ok | failed | skipped | stale
	At     string `json:"at,omitempty"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// RefetchResult is the response of POST /api/v1/sources/{hash}/refetch: a forced fetch of an attached
// URL. A readable page replaces the snapshot and clears the marker; a wall or a failure keeps the
// attached snapshot exactly as it was.
type RefetchResult struct {
	Replaced bool   `json:"replaced"`
	Blocked  bool   `json:"blocked"`
	Failed   bool   `json:"failed"`
	Detail   string `json:"detail,omitempty"`
}

// SourceList is the response of GET /api/v1/sources.
type SourceList struct {
	Sources []Source `json:"sources"`
	// Count is the TOTAL the list holds, not len(Sources): a page has to know that more exist.
	Count int `json:"count"`
	Total int `json:"total"`
	// Command is set when the list is an assistant result: the record it came from, whether it has
	// expired, and how many of its members a deleted source took with it.
	Command *CommandRef `json:"command,omitempty"`
	// Queue is the read-later queue's split, present only when the list IS the queue: the open
	// entries and how many of them a rule queued. The list's `count` is the open entries too.
	Queue *QueueCounts `json:"queue,omitempty"`
}

// QueueCounts is the queue's own split: the open entries and each rule's share (by slug; the client
// knows the names). An entry can carry more than one rule, so the shares may sum past the open
// count — that is what fired, not a partition.
type QueueCounts struct {
	Open      int             `json:"open"`
	Interests []InterestShare `json:"interests,omitempty"`
}

// InterestShare is one rule's share of the queue.
type InterestShare struct {
	Slug  string `json:"slug"`
	Count int    `json:"count"`
}

// SourceTagFacet is one tag of the Sources cloud and how many sources carry it.
type SourceTagFacet struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// SourceFacets is the Sources lens's facet payload: the tag cloud and the vault-wide counts behind
// the filter chips. All of them are vault facts, not facts about the window the list has loaded.
type SourceFacets struct {
	Tags     []SourceTagFacet `json:"tags"`
	Attached int              `json:"attached"`
	// Changed is how many sources carry a change time: the "updated" filter chip's count.
	Changed int `json:"changed"`
	// Favorites is the starred sources; ReadLater is the OPEN queue; ReadLaterInterests is how much
	// of that queue the interests put there ("4 von deinen Interessen").
	Favorites          int `json:"favorites"`
	ReadLater          int `json:"read_later"`
	ReadLaterInterests int `json:"read_later_interests"`
}

// SourceTagsRequest is the body of PUT /api/v1/sources/{hash}/tags.
type SourceTagsRequest struct {
	Tags []string `json:"tags"`
}

// AddRequest is the body of POST /api/v1/add.
type AddRequest struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	// Bookmark forces a URL to be stored as a collected link (no indexing), even if
	// the page has readable text.
	Bookmark bool `json:"bookmark"`
	// Listing answers the listing question for a URL. Absent means "detect and OFFER": the page is
	// stored as it is and the answer says how many article cards were found. true turns the page
	// into a listing (the reader confirmed the offer), false never treats it as one.
	Listing *bool `json:"listing,omitempty"`
}

// AddURLsRequest is the body of POST /api/v1/add-urls (one or many URLs).
type AddURLsRequest struct {
	URLs []string `json:"urls"`
}

// AddURLsResult is the response of POST /api/v1/add-urls.
type AddURLsResult struct {
	Accepted int `json:"accepted"`
}

// ReindexResult is the response of POST /api/v1/reindex.
type ReindexResult struct {
	Scanned int `json:"scanned"`
	Indexed int `json:"indexed"`
	Added   int `json:"added"`
	// Removed counts sources whose object is gone; AtomsRemoved counts claims whose FILE is gone (their
	// rows and usage records go with them — the vault is the truth, the index is rebuilt from it).
	Removed      int `json:"removed"`
	AtomsRemoved int `json:"atoms_removed"`
	// AtomsOrphaned counts claims trashed because every source they cited is gone from the vault, and
	// ProvenancePruned the claims that lost only a dead source. The pipeline is complete or it is
	// nothing: a claim cannot hang on a source that is not there.
	AtomsOrphaned    int `json:"atoms_orphaned"`
	ProvenancePruned int `json:"provenance_pruned"`
	// Clusters counts the association records mirrored into the index, and ClustersRemoved the rows or
	// records that had no claim left.
	Clusters        int `json:"clusters"`
	ClustersRemoved int `json:"clusters_removed"`
	Atoms           int `json:"atoms"`
	// Steps counts the sources whose document-step state was mirrored from their .meta file, and
	// PartsPruned the stale part records removed (a node that is gone or has no text).
	Steps       int `json:"steps"`
	PartsPruned int `json:"parts_pruned"`
	Skipped     int `json:"skipped"`
	Orphans     int `json:"orphans"`
}

// RefreshResult is the response of POST /api/v1/refresh-urls.
type RefreshResult struct {
	Checked int `json:"checked"`
	Changed int `json:"changed"`
	Failed  int `json:"failed"`
	// Blocked counts pages that answered with a wall (a login, a paywall, a consent gate): the source
	// is fine, the site is not answering with itself.
	Blocked int `json:"blocked,omitempty"`
	// Attached counts sources with a human-supplied snapshot. The pass leaves them alone; a forced
	// per-source refetch is the explicit way to try one.
	Attached int `json:"attached,omitempty"`
	// Discovered counts the NEW pages a sitemap or a feed declared in this pass (already claimed);
	// Removed counts the pages a sitemap no longer lists. A feed never removes.
	Discovered int `json:"discovered,omitempty"`
	Removed    int `json:"removed,omitempty"`
	// Detail explains a failure of a ONE-SOURCE refresh (the per-source button). A pass reports the
	// count; one source has a reason the reader can act on.
	Detail string `json:"detail,omitempty"`
}

// DeleteResult is the response of DELETE /api/v1/sources/{hash}.
type DeleteResult struct {
	Hash              string `json:"hash"`
	AtomsRemoved      int    `json:"atoms_removed"`
	DependentsRemoved int    `json:"dependents_removed,omitempty"`
}

// CaptureResult is the response of POST /api/v1/sources/{hash}/capture.
type CaptureResult struct {
	// Queued is true when step 1 was scheduled (a source whose bytes are not readable, or one whose
	// title should be derived again from the object).
	Queued bool `json:"queued"`
	// Detail says what happened when nothing was scheduled.
	Detail string `json:"detail,omitempty"`
}

// ReconcileResult is the response of POST /api/v1/reconcile (the consistency job, run by hand).
type ReconcileResult struct {
	Checked     int      `json:"checked"`
	Invalidated []string `json:"invalidated,omitempty"`
	Broken      []string `json:"broken,omitempty"`
	// Languages counts old sources whose missing language hint was detected in this pass.
	Languages int `json:"languages,omitempty"`
	// Identities counts source records that had lost what the source is and were restored from the
	// index; Ownerships counts rows that had lost their owner and were restored from the record.
	Identities int `json:"identities,omitempty"`
	Ownerships int `json:"ownerships,omitempty"`
	// Summaries counts sources whose derivation is complete but whose overview is missing. The app
	// queues them on its own worker; the check itself writes no summary.
	Summaries int `json:"summaries,omitempty"`
}

// ClusterResult is the response of POST /api/v1/cluster (the association step, queued by hand).
type ClusterResult struct {
	Queued bool   `json:"queued"`
	RunID  string `json:"run_id,omitempty"`
}

// SourceReset reports what a reset threw away.
type SourceReset struct {
	Hash     string `json:"hash"`
	Atoms    int    `json:"atoms"`
	Nodes    int    `json:"nodes"`
	Bookmark bool   `json:"bookmark,omitempty"`
}

// SourceLink is one resolved link: the address a document wrote and the source the vault holds for
// it. Only URLs that ARE sources appear in the answer.
type SourceLink struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

// SourceLinkList is the answer of GET /sources/resolve.
type SourceLinkList struct {
	Links []SourceLink `json:"links"`
}

// SourceLinksFrom is the use case's answer on the wire.
func SourceLinksFrom(in []entities.SourceLink) SourceLinkList {
	out := make([]SourceLink, 0, len(in))
	for _, link := range in {
		out = append(out, SourceLink{URL: link.URL, Hash: link.Hash})
	}
	return SourceLinkList{Links: out}
}
