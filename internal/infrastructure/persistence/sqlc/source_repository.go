// Package sqlc adapts the generated queries to the core IndexRepository port.
package sqlc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc/dal"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// SourceRepository implements interfaces.IndexRepository.
type SourceRepository struct {
	db *sql.DB
	q  *dal.Queries
}

var _ interfaces.IndexRepository = (*SourceRepository)(nil)

// NewSourceRepository wraps a database handle.
func NewSourceRepository(db *sql.DB) *SourceRepository {
	return &SourceRepository{db: db, q: dal.New(db)}
}

// UpsertSource inserts or refreshes a source row. On conflict only derived columns
// are refreshed, so ids, titles, provenance and tree state survive a reindex.
func (r *SourceRepository) UpsertSource(ctx context.Context, s entities.Source) error {
	meta := withMetaFields(s.Meta, map[string]string{"language": s.Language, "origin": s.Origin, "wiki_page": s.WikiPage, "parent_hash": s.ParentHash, "sitemap": boolField(s.Sitemap), "rss": boolField(s.RSS), "from_feed": boolField(s.FromFeed), "feed_hash": s.FeedHash})
	err := r.q.UpsertSource(ctx, dal.UpsertSourceParams{
		ID:         s.ID,
		Hash:       s.Hash,
		Kind:       string(s.Kind),
		Ext:        s.Ext,
		Path:       s.Path,
		Url:        nullString(s.URL),
		Title:      s.Title,
		Bytes:      s.Bytes,
		Pages:      nullInt(s.Pages),
		TreeState:  string(s.TreeState),
		ImportedAt: s.ImportedAt.UnixMilli(),
		Meta:       meta,
		Bookmark:   boolInt(s.Bookmark),
		// The raw input's own name: without this the column stayed empty and the title silently
		// stood in for it (a fallback that hid the miss).
		OriginalName: s.OriginalName,
	})
	if err != nil {
		return fmt.Errorf("upsert source: %w", err)
	}
	return nil
}

// FillURLSource completes a QUEUED url source: the row and its record existed before anything was
// fetched (a wiki page linked the URL), so the fetch writes what it learned into that row — the title
// the document named itself, the bookmark decision, the snapshot metadata — without moving it to
// another address. UpsertSource cannot do this: it deliberately keeps the identity a row was created
// with, which is right for a reindex and wrong for a claim on a URL that was never fetched.
func (r *SourceRepository) FillURLSource(ctx context.Context, s entities.Source) error {
	err := r.q.FillURLSource(ctx, dal.FillURLSourceParams{
		Ext:          s.Ext,
		Path:         s.Path,
		Bytes:        s.Bytes,
		Title:        s.Title,
		OriginalName: s.OriginalName,
		Bookmark:     boolInt(s.Bookmark),
		Meta:         withMetaFields(s.Meta, map[string]string{"language": s.Language, "origin": s.Origin, "wiki_page": s.WikiPage, "parent_hash": s.ParentHash, "sitemap": boolField(s.Sitemap), "rss": boolField(s.RSS), "from_feed": boolField(s.FromFeed), "feed_hash": s.FeedHash}),
		Hash:         s.Hash,
	})
	if err != nil {
		return fmt.Errorf("fill url source: %w", err)
	}
	return nil
}

// SetSourceHash moves a source to the address it should have had — a URL source to its URL's hash. The
// row keeps its ID, which is the whole point: everything that references the source by ID (its
// provenance rows above all) survives the move, so no claim loses its source.
func (r *SourceRepository) SetSourceHash(ctx context.Context, oldHash, newHash, path string) error {
	if err := r.q.SetSourceHash(ctx, dal.SetSourceHashParams{
		Hash: newHash, Path: path, Hash_2: oldHash,
	}); err != nil {
		return fmt.Errorf("set source hash: %w", err)
	}
	return nil
}

// SetSourceMeta writes a source's metadata. It is the one column a reindex must be able to restore:
// a URL snapshot's content hash and its icon live there, and losing them (a deleted index.db) used to
// make every URL look changed to the next refresh.
//
// OWNERSHIP IS NOT A SNAPSHOT'S TO WRITE. `origin`, `wiki_page`, `parent_hash` and `sitemap` live ONLY
// in this JSON, and they decide who may remove a source: a write that rebuilt the JSON from the
// snapshot's own fields silently un-owned every link a wiki page had brought in — and a reindex
// reinforced it, because it wrote the ownership first and this meta second. They are carried over
// from the row unless the new JSON names them itself.
func (r *SourceRepository) SetSourceMeta(ctx context.Context, hash, meta string) error {
	if meta == "" {
		return nil
	}
	var current string
	if err := r.db.QueryRowContext(ctx, `SELECT meta FROM sources WHERE hash = ?`, hash).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read source meta: %w", err)
	}
	meta = keepOwnership(current, meta)
	if err := r.q.SetSourceMeta(ctx, dal.SetSourceMetaParams{Meta: meta, Hash: hash}); err != nil {
		return fmt.Errorf("set source meta: %w", err)
	}
	return nil
}

// keepOwnership copies the row's ownership keys into a meta write that does not name them. A key the
// new JSON DOES name (even with null) is the caller's decision and is left alone.
func keepOwnership(current, next string) string {
	if current == "" || current == next {
		return next
	}
	var cur, nxt map[string]any
	if json.Unmarshal([]byte(current), &cur) != nil || json.Unmarshal([]byte(next), &nxt) != nil || nxt == nil {
		return next
	}
	changed := false
	for _, key := range []string{"origin", "wiki_page", "parent_hash", "sitemap", "rss"} {
		if _, owned := nxt[key]; owned {
			continue
		}
		if value, ok := cur[key]; ok {
			nxt[key] = value
			changed = true
		}
	}
	if !changed {
		return next
	}
	b, err := json.Marshal(nxt)
	if err != nil {
		return next
	}
	return string(b)
}

// SetSourceSteps mirrors a source's .meta file into the index. The file stays the truth; this is
// what makes the list query answer "which steps is this document through?" without reading a file
// per row.
func (r *SourceRepository) SetSourceSteps(ctx context.Context, hash string, steps entities.StepSet) error {
	b, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	if err := r.q.SetSourceSteps(ctx, dal.SetSourceStepsParams{Steps: string(b), Hash: hash}); err != nil {
		return fmt.Errorf("set source steps: %w", err)
	}
	return nil
}

// SetSourceTitle records a source's title. An empty title is ignored: a step that could not
// determine one must not wipe the title another step already found.
func (r *SourceRepository) SetSourceTitle(ctx context.Context, hash, title string) error {
	if strings.TrimSpace(title) == "" {
		return nil
	}
	if err := r.q.SetSourceTitle(ctx, dal.SetSourceTitleParams{Title: title, Hash: hash}); err != nil {
		return fmt.Errorf("set source title: %w", err)
	}
	return nil
}

// decodeStepSet reads the mirrored step state. A malformed value is an empty set, not an error:
// the .meta file is the truth and the next write repairs the mirror.
func decodeStepSet(raw string) entities.StepSet {
	if strings.TrimSpace(raw) == "" {
		return entities.NewStepSet()
	}
	var set entities.StepSet
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		return entities.NewStepSet()
	}
	if set == nil {
		return entities.NewStepSet()
	}
	return set
}

// SetSourceLanguage writes the language hint into the source's free-form metadata. The language
// lives in the .meta record as well; this is the index mirror the search reads without opening a
// file per source.
func (r *SourceRepository) SetSourceOrigin(ctx context.Context, hash, origin, wikiPage, parentHash string) error {
	if strings.TrimSpace(origin) == "" {
		return nil
	}
	var raw string
	if err := r.db.QueryRowContext(ctx, `SELECT meta FROM sources WHERE hash = ?`, hash).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.New("not_found", "no such source", types.ErrNotFound)
		}
		return fmt.Errorf("read source meta: %w", err)
	}
	return r.SetSourceMeta(ctx, hash, withMetaFields(raw, map[string]string{
		"origin": origin, "wiki_page": wikiPage, "parent_hash": parentHash,
	}))
}

// ClearSourceOrigin removes a source's ownership from the index metadata. It cannot go through
// SetSourceMeta, which deliberately keeps ownership keys a write does not name — this is the one
// caller that means to remove them.
func (r *SourceRepository) ClearSourceOrigin(ctx context.Context, hash string) error {
	var raw string
	if err := r.db.QueryRowContext(ctx, `SELECT meta FROM sources WHERE hash = ?`, hash).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("read source meta: %w", err)
	}
	m := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	delete(m, "origin")
	delete(m, "wiki_page")
	delete(m, "parent_hash")
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("clear source origin: %w", err)
	}
	if err := r.q.SetSourceMeta(ctx, dal.SetSourceMetaParams{Meta: string(b), Hash: hash}); err != nil {
		return fmt.Errorf("clear source origin: %w", err)
	}
	return nil
}

func (r *SourceRepository) SetSourceLanguage(ctx context.Context, hash, language string) error {
	if strings.TrimSpace(language) == "" {
		return nil
	}
	var raw string
	if err := r.db.QueryRowContext(ctx, `SELECT meta FROM sources WHERE hash = ?`, hash).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.New("not_found", "no such source", types.ErrNotFound)
		}
		return fmt.Errorf("read source meta: %w", err)
	}
	return r.SetSourceMeta(ctx, hash, withMetaLanguage(raw, language))
}

// SourceLanguages returns every source's language hint. It is the SET the search uses; it is read
// from the index mirror, so it stays cheap even when a source file has to be opened for another
// reason.
func (r *SourceRepository) SourceLanguages(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT hash, meta FROM sources`)
	if err != nil {
		return nil, fmt.Errorf("source languages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var hash, raw string
		if err := rows.Scan(&hash, &raw); err != nil {
			return nil, err
		}
		if lang := metaLanguage(raw); lang != "" {
			out[hash] = lang
		}
	}
	return out, rows.Err()
}

func withMetaLanguage(raw, language string) string {
	return withMetaFields(raw, map[string]string{"language": language})
}

func withMetaFields(raw string, fields map[string]string) string {
	m := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	for key, value := range fields {
		value = strings.TrimSpace(value)
		if key != "wiki_page" {
			value = strings.ToLower(value)
		}
		if value != "" {
			m[key] = value
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

func metaLanguage(raw string) string {
	return metaField(raw, "language")
}

func metaField(raw, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	v = strings.TrimSpace(v)
	if key != "wiki_page" {
		v = strings.ToLower(v)
	}
	return v
}

// GetSourceByHash returns a source by its content hash.
func (r *SourceRepository) GetSourceByHash(ctx context.Context, hash string) (entities.Source, error) {
	row, err := r.q.GetSourceByHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return entities.Source{}, types.New("not_found", "no such source", types.ErrNotFound)
	}
	if err != nil {
		return entities.Source{}, fmt.Errorf("get source: %w", err)
	}
	out := toEntity(row)
	// The derived numbers too, exactly as the list computes them: the detail view is opened from a
	// row, and showing zeros there (parts done, claims, open claims) made the same source read
	// "pending distill" in its own overlay while the row said "complete".
	if c, err := r.q.SourceDerivedCounts(ctx, hash); err == nil {
		out.AtomCount = int(c.AtomCount)
		out.ProvenanceRows = int(c.ProvenanceRows)
		out.PartsTotal = int(c.PartsTotal)
		out.PartsDone = int(c.PartsDone)
		out.PartsDerived = int(c.PartsDerived)
		out.AtomsLeft = int(c.AtomsLeft)
	}
	return out, nil
}

// ListSourceChildren returns the sources one source owns: the pages of a sitemap (parent_hash) plus
// the URLs a wiki page brought in (wiki_page). One owner is set, so the two sets do not overlap; the
// merge is by hash anyway, so a future source owned twice cannot be listed twice.
func (r *SourceRepository) ListSourceChildren(ctx context.Context, parentHash, wikiPage string) ([]entities.Source, error) {
	out := make([]entities.Source, 0, 8)
	seen := map[string]bool{}
	if parentHash != "" {
		rows, err := r.q.ListSourcesByParent(ctx, parentHash)
		if err != nil {
			return nil, fmt.Errorf("list source children: %w", err)
		}
		for _, src := range rows {
			if seen[src.Hash] {
				continue
			}
			seen[src.Hash] = true
			out = append(out, toEntity(src))
		}
	}
	if wikiPage != "" {
		rows, err := r.q.ListSourcesByWikiPage(ctx, wikiPage)
		if err != nil {
			return nil, fmt.Errorf("list wiki children: %w", err)
		}
		for _, src := range rows {
			if seen[src.Hash] {
				continue
			}
			seen[src.Hash] = true
			out = append(out, toEntity(src))
		}
	}
	return out, nil
}

// CountSourceChildren is the number a source detail uses to decide whether it has a sub-sources tab
// at all: one query per owner, never a walk of every source.
func (r *SourceRepository) CountSourceChildren(ctx context.Context, parentHash, wikiPage string) (int, error) {
	total := int64(0)
	if parentHash != "" {
		n, err := r.q.CountSourcesByParent(ctx, parentHash)
		if err != nil {
			return 0, fmt.Errorf("count source children: %w", err)
		}
		total += n
	}
	if wikiPage != "" {
		n, err := r.q.CountSourcesByWikiPage(ctx, wikiPage)
		if err != nil {
			return 0, fmt.Errorf("count wiki children: %w", err)
		}
		total += n
	}
	return int(total), nil
}

// SetSourceTags mirrors a source's human labels into the index. The .meta record is written first by
// the tracker; this is the derived mirror the list and the vocabulary read.
func (r *SourceRepository) SetSourceTags(ctx context.Context, hash string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("encode source tags: %w", err)
	}
	if err := r.q.SetSourceTags(ctx, dal.SetSourceTagsParams{Tags: string(raw), Hash: hash}); err != nil {
		return fmt.Errorf("set source tags: %w", err)
	}
	return nil
}

// SetSourceAutoTags mirrors the agent's labels (and when they were derived) into the index. The
// .meta record is written first by the tracker; this is the derived mirror.
func (r *SourceRepository) SetSourceAutoTags(ctx context.Context, hash string, tags []string, at time.Time) error {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("encode source auto tags: %w", err)
	}
	params := dal.SetSourceAutoTagsParams{AutoTags: string(raw), Hash: hash}
	if !at.IsZero() {
		params.AutoTagsAt = sql.NullInt64{Int64: at.UnixMilli(), Valid: true}
	}
	if err := r.q.SetSourceAutoTags(ctx, params); err != nil {
		return fmt.Errorf("set source auto tags: %w", err)
	}
	return nil
}

// SourceTagCounts is the OBSERVED source-tag catalog: every human and derived label, counted once per
// source (a source that carries a label as both human and auto is one use, like a wiki page).
func (r *SourceRepository) SourceTagCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.q.ListSourceTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("list source tags: %w", err)
	}
	out := map[string]int{}
	for _, row := range rows {
		seen := map[string]bool{}
		for _, list := range [][]string{decodeSourceTags(row.Tags), decodeSourceTags(row.AutoTags)} {
			for _, tag := range list {
				if tag == "" || seen[tag] {
					continue
				}
				seen[tag] = true
				out[tag]++
			}
		}
	}
	return out, nil
}

func timeFromNull(v sql.NullInt64) time.Time {
	if !v.Valid || v.Int64 <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64).UTC()
}

func decodeSourceTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

// CountSources is how many sources the FILTERED list has in total, so a page knows whether it is
// the whole list and a windowed scroller knows its full height.
func (r *SourceRepository) CountSources(ctx context.Context, filter entities.SourceFilter) (int, error) {
	n, err := r.q.CountSourcesFiltered(ctx, dal.CountSourcesFilteredParams{
		Kind:      filter.Kind,
		Query:     filter.Query,
		Tag:       filter.Tag,
		Attached:  boolParam(filter.AttachedOnly),
		Bookmarks: boolParam(filter.BookmarksOnly),
		Failed:    boolParam(filter.FailedOnly),
	})
	if err != nil {
		return 0, fmt.Errorf("count sources: %w", err)
	}
	return int(n), nil
}

// CountAttachedSources is how many sources carry a human-attached snapshot.
func (r *SourceRepository) CountAttachedSources(ctx context.Context) (int, error) {
	n, err := r.q.CountAttachedSources(ctx)
	if err != nil {
		return 0, fmt.Errorf("count attached sources: %w", err)
	}
	return int(n), nil
}

// boolParam turns the optional filter into the integer sentinel the generated query expects.
func boolParam(only bool) int64 {
	if only {
		return 1
	}
	return 0
}

// ListSources returns sources ordered by import time (newest first), narrowed by filter.
func (r *SourceRepository) ListSources(ctx context.Context, filter entities.SourceFilter, limit, offset int) ([]entities.Source, error) {
	rows, err := r.q.ListSourcesFiltered(ctx, dal.ListSourcesFilteredParams{
		Kind:      filter.Kind,
		Query:     filter.Query,
		Tag:       filter.Tag,
		Attached:  boolParam(filter.AttachedOnly),
		Bookmarks: boolParam(filter.BookmarksOnly),
		Failed:    boolParam(filter.FailedOnly),
		Limit:     int64(limit),
		Offset:    int64(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	out := make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEntity(row))
	}
	// The aggregates of the PAGE, not of the table. The five whole-table GROUP BYs (170k provenance
	// rows, 248k memberships) answered every list request — a 20-row page paid for the vault and the
	// Sources lens took a second per refresh. The page's ids scope every one of them.
	if len(out) > 0 {
		ids := make([]ulid.ULID, 0, len(out))
		raw := make([][]byte, 0, len(out))
		for i := range out {
			ids = append(ids, out[i].ID)
			raw = append(raw, out[i].ID[:])
		}
		if counts, err := r.q.SourceAtomCountsForSources(ctx, ids); err == nil {
			m := make(map[ulid.ULID]int, len(counts))
			for _, c := range counts {
				m[c.SourceID] = int(c.Count)
			}
			for i := range out {
				out[i].AtomCount = m[out[i].ID]
			}
		}
		// What is left to do for each source: parts finished, and claims still without a cluster.
		if totals, err := r.q.SourcePartTotalsForSources(ctx, raw); err == nil {
			m := make(map[ulid.ULID]int, len(totals))
			for _, c := range totals {
				m[idFromBytes(c.SourceID)] = int(c.Count)
			}
			for i := range out {
				out[i].PartsTotal = m[out[i].ID]
			}
		}
		if done, err := r.q.SourcePartsDoneForSources(ctx, raw); err == nil {
			type partCounts struct{ done, derived int }
			m := make(map[ulid.ULID]partCounts, len(done))
			for _, c := range done {
				m[idFromBytes(c.SourceID)] = partCounts{done: int(c.Count), derived: int(c.Derived)}
			}
			for i := range out {
				out[i].PartsDone = m[out[i].ID].done
				out[i].PartsDerived = m[out[i].ID].derived
			}
		}
		if left, err := r.q.SourceAtomsLeftForSources(ctx, ids); err == nil {
			m := make(map[ulid.ULID]int, len(left))
			for _, c := range left {
				m[c.SourceID] = int(c.Count)
			}
			for i := range out {
				out[i].AtomsLeft = m[out[i].ID]
			}
		}
		// The raw provenance rows too: when they exceed the resolvable atoms, the index is damaged
		// and the UI can say so instead of showing a number nobody can reproduce.
		if rows, err := r.q.SourceProvenanceCountsForSources(ctx, ids); err == nil {
			m := make(map[ulid.ULID]int, len(rows))
			for _, c := range rows {
				m[c.SourceID] = int(c.Count)
			}
			for i := range out {
				out[i].ProvenanceRows = m[out[i].ID]
			}
		}
	}
	return out, nil
}

// ListRecentSources returns the newest CONTENT sources in a recency window. Bookmarks and
// sitemap/feed containers are counted, never listed: they are navigation, not knowledge, and have
// no derivation to summarise. The window is inclusive; a zero `since` means the whole vault.
func (r *SourceRepository) ListRecentSources(ctx context.Context, since time.Time, limit, offset int) (interfaces.RecentSources, error) {
	var out interfaces.RecentSources
	if limit <= 0 {
		limit = 8
	}
	if offset < 0 {
		offset = 0
	}
	sinceMS := int64(0)
	if !since.IsZero() {
		sinceMS = since.UnixMilli()
	}
	rows, err := r.q.ListRecentContentSources(ctx, dal.ListRecentContentSourcesParams{
		Since:  sinceMS,
		Offset: int64(offset),
		Limit:  int64(limit),
	})
	if err != nil {
		return out, fmt.Errorf("list recent sources: %w", err)
	}
	ids := make([]ulid.ULID, 0, len(rows))
	out.Sources = make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		src := toEntity(row)
		out.Sources = append(out.Sources, src)
		ids = append(ids, src.ID)
	}
	total, err := r.q.CountRecentContentSources(ctx, sinceMS)
	if err != nil {
		return out, fmt.Errorf("count recent sources: %w", err)
	}
	out.Total = int(total)
	excluded, err := r.q.CountRecentExcludedSources(ctx, sinceMS)
	if err != nil {
		return out, fmt.Errorf("count excluded recent sources: %w", err)
	}
	out.Bookmarks = int(excluded.Bookmarks)
	out.Containers = int(excluded.Containers)
	// The digest's claim count is a derived number like the list's: the page's ids scope it.
	if len(ids) > 0 {
		if counts, cErr := r.q.SourceAtomCountsForSources(ctx, ids); cErr == nil {
			m := make(map[ulid.ULID]int, len(counts))
			for _, c := range counts {
				m[c.SourceID] = int(c.Count)
			}
			for i := range out.Sources {
				out.Sources[i].AtomCount = m[out.Sources[i].ID]
			}
		}
	}
	return out, nil
}

// DeleteSourceByHash removes a source row (reindex reconciliation).
func (r *SourceRepository) DeleteSourceByHash(ctx context.Context, hash string) error {
	if err := r.q.DeleteSourceByHash(ctx, hash); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	return nil
}

// SourceHashes returns every indexed hash.
func (r *SourceRepository) SourceHashes(ctx context.Context) ([]string, error) {
	hashes, err := r.q.ListSourceHashes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list source hashes: %w", err)
	}
	return hashes, nil
}

// SourceKinds returns every indexed source's kind, keyed by hash. The reindex reads it as the
// fallback for a source whose record (<hash>.meta) predates the identity being recorded there: the
// object's file name cannot tell a URL snapshot from a Markdown source (both are <hash>.md), but the
// index row already knows.
func (r *SourceRepository) SourceKinds(ctx context.Context) (map[string]string, error) {
	rows, err := r.q.ListSourceKinds(ctx)
	if err != nil {
		return nil, fmt.Errorf("list source kinds: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Hash] = row.Kind
	}
	return out, nil
}

// SetSourceKindURL marks a source as a URL snapshot and records its address.
func (r *SourceRepository) SetSourceKindURL(ctx context.Context, hash, url string) error {
	if err := r.q.SetSourceKindURL(ctx, dal.SetSourceKindURLParams{Url: nullString(url), Hash: hash}); err != nil {
		return fmt.Errorf("set source kind url: %w", err)
	}
	return nil
}

// SourceHasAtoms reports whether any atom cites this source (derived status).
func (r *SourceRepository) SourceHasAtoms(ctx context.Context, hash string) (bool, error) {
	n, err := r.q.SourceAtomCount(ctx, hash)
	if err != nil {
		return false, fmt.Errorf("source atom count: %w", err)
	}
	return n > 0, nil
}

// SourcesWithoutAtoms lists sources with a built tree but no atoms yet.
func (r *SourceRepository) SourcesWithoutAtoms(ctx context.Context) ([]entities.Source, error) {
	rows, err := r.q.ListSourcesWithoutAtoms(ctx)
	if err != nil {
		return nil, fmt.Errorf("sources without atoms: %w", err)
	}
	out := make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEntity(row))
	}
	return out, nil
}

// SourcesWithOpenParts lists sources whose text parts are not all derived yet.
func (r *SourceRepository) SourcesWithOpenParts(ctx context.Context) ([]entities.Source, error) {
	rows, err := r.q.ListSourcesWithOpenParts(ctx)
	if err != nil {
		return nil, fmt.Errorf("sources with open parts: %w", err)
	}
	out := make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEntity(row))
	}
	return out, nil
}

// SourceOpenParts counts the text parts of one source that no done record covers.
func (r *SourceRepository) SourceOpenParts(ctx context.Context, hash string) (int, error) {
	n, err := r.q.CountSourceOpenParts(ctx, hash)
	if err != nil {
		return 0, fmt.Errorf("source open parts: %w", err)
	}
	return int(n), nil
}

// SourcesWithUnclustered lists sources whose claims no cluster owns yet.
func (r *SourceRepository) SourcesWithUnclustered(ctx context.Context) ([]entities.Source, error) {
	rows, err := r.q.ListSourcesWithUnclustered(ctx)
	if err != nil {
		return nil, fmt.Errorf("sources with unclustered claims: %w", err)
	}
	out := make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEntity(row))
	}
	return out, nil
}

// SourcesWithNoTree lists sources whose tree was never built.
func (r *SourceRepository) SourcesWithNoTree(ctx context.Context) ([]entities.Source, error) {
	rows, err := r.q.ListSourcesWithNoTree(ctx)
	if err != nil {
		return nil, fmt.Errorf("sources with no tree: %w", err)
	}
	out := make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEntity(row))
	}
	return out, nil
}

// SetSourceURLState records the last HTTP status and check time of a URL source.
func (r *SourceRepository) SetSourceURLState(ctx context.Context, hash string, status int, checkedAt time.Time) error {
	err := r.q.SetSourceURLState(ctx, dal.SetSourceURLStateParams{
		UrlStatus:    sql.NullInt64{Int64: int64(status), Valid: status > 0},
		UrlCheckedAt: sql.NullInt64{Int64: checkedAt.UnixMilli(), Valid: !checkedAt.IsZero()},
		Hash:         hash,
	})
	if err != nil {
		return fmt.Errorf("set source url state: %w", err)
	}
	return nil
}

// ListURLSources lists every URL snapshot (newest first).
func (r *SourceRepository) ListURLSources(ctx context.Context) ([]entities.Source, error) {
	rows, err := r.q.ListURLSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list url sources: %w", err)
	}
	out := make([]entities.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEntity(row))
	}
	return out, nil
}

// LatestSourceByURL returns the newest snapshot for a URL.
func (r *SourceRepository) LatestSourceByURL(ctx context.Context, url string) (entities.Source, error) {
	row, err := r.q.LatestSourceByURL(ctx, nullString(url))
	if errors.Is(err, sql.ErrNoRows) {
		return entities.Source{}, types.New("not_found", "no source for this url", types.ErrNotFound)
	}
	if err != nil {
		return entities.Source{}, fmt.Errorf("latest source by url: %w", err)
	}
	return toEntity(row), nil
}

func toEntity(row dal.Source) entities.Source {
	s := entities.Source{
		ID:           row.ID,
		Hash:         row.Hash,
		Kind:         entities.SourceKind(row.Kind),
		Ext:          row.Ext,
		Path:         row.Path,
		Title:        row.Title,
		Language:     metaLanguage(row.Meta),
		Origin:       metaField(row.Meta, "origin"),
		WikiPage:     metaField(row.Meta, "wiki_page"),
		ParentHash:   metaField(row.Meta, "parent_hash"),
		Sitemap:      metaField(row.Meta, "sitemap") == "true",
		RSS:          metaField(row.Meta, "rss") == "true",
		FromFeed:     metaField(row.Meta, "from_feed") == "true",
		FeedHash:     metaField(row.Meta, "feed_hash"),
		Wall:         metaField(row.Meta, "wall"),
		OriginalName: row.OriginalName,
		Bytes:        row.Bytes,
		TreeState:    entities.TreeState(row.TreeState),
		ImportedAt:   time.UnixMilli(row.ImportedAt).UTC(),
		Meta:         row.Meta,
		Tags:         decodeSourceTags(row.Tags),
		AutoTags:     decodeSourceTags(row.AutoTags),
		AutoTagsAt:   timeFromNull(row.AutoTagsAt),
		Steps:        decodeStepSet(row.Steps),
	}
	// OriginalName stays EMPTY when it is not recorded. It is the raw input's own name — the file
	// name, or the URL — and the title must never stand in for it: the two are different things, and
	// a title in that place is what made a row read as its own duplicate. An empty value is honest;
	// the reader falls back to the URL, then to the hash.
	if row.Url.Valid {
		s.URL = row.Url.String
	}
	if row.Pages.Valid {
		s.Pages = int(row.Pages.Int64)
	}
	if row.UrlStatus.Valid {
		s.URLStatus = int(row.UrlStatus.Int64)
	}
	if row.UrlCheckedAt.Valid {
		s.URLChecked = time.UnixMilli(row.UrlCheckedAt.Int64).UTC()
	}
	s.Bookmark = row.Bookmark != 0
	// The attachment is identity, and it lives in the meta JSON as a BOOL (metaField reads strings
	// only). It is a wire projection for the source page; the refresh still decides from the meta.
	if s.Kind == entities.SourceKindURL {
		var attachment struct {
			Attached     bool   `json:"attached"`
			AttachedFrom string `json:"attached_from"`
		}
		if json.Unmarshal([]byte(row.Meta), &attachment) == nil {
			s.Attached, s.AttachedFrom = attachment.Attached, attachment.AttachedFrom
		}
	}
	return s
}

func boolField(v bool) string {
	if v {
		return "true"
	}
	return ""
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(n int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(n), Valid: n > 0}
}

// ResetSourceDerived restores the derived source fields to their intake state (title from the
// file name, unknown page count), so a reset really looks like a fresh intake.
func (r *SourceRepository) ResetSourceDerived(ctx context.Context, hash, title string) error {
	if err := r.q.ResetSourceDerived(ctx, dal.ResetSourceDerivedParams{Title: title, Hash: hash}); err != nil {
		return fmt.Errorf("reset source derived: %w", err)
	}
	return nil
}

// SetSourceTree records the tree state and refreshes derived source fields.
func (r *SourceRepository) SetSourceTree(ctx context.Context, hash, state, title string, pages int) error {
	if err := r.q.SetSourceTreeState(ctx, dal.SetSourceTreeStateParams{TreeState: state, Hash: hash}); err != nil {
		return fmt.Errorf("set tree state: %w", err)
	}
	if title != "" {
		if err := r.q.SetSourceTitle(ctx, dal.SetSourceTitleParams{Title: title, Hash: hash}); err != nil {
			return fmt.Errorf("set title: %w", err)
		}
	}
	if pages > 0 {
		if err := r.q.SetSourcePages(ctx, dal.SetSourcePagesParams{Pages: nullInt(pages), Hash: hash}); err != nil {
			return fmt.Errorf("set pages: %w", err)
		}
	}
	return nil
}

// ReplaceSourceNodes swaps a source's node rows atomically.
func (r *SourceRepository) ReplaceSourceNodes(ctx context.Context, sourceID ulid.ULID, nodes []entities.SourceNode) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := r.q.WithTx(tx)
	if err := q.DeleteSourceNodes(ctx, sourceID[:]); err != nil {
		return fmt.Errorf("delete nodes: %w", err)
	}
	for _, n := range nodes {
		// text_hash is the index's answer to "does this node carry text?": empty for a node without
		// any. The distill work unit is one text-bearing node, and counting them needs that signal.
		// A node from the outline carries its hash (it is part of the reference); a node built in
		// memory carries its text. Either way the index gets the same answer.
		textHash := n.TextHash
		if textHash == "" && strings.TrimSpace(n.Text) != "" {
			sum := sha256.Sum256([]byte(n.Text))
			textHash = hex.EncodeToString(sum[:])
		}
		if err := q.InsertSourceNode(ctx, dal.InsertSourceNodeParams{
			SourceID:  sourceID[:],
			NodeID:    n.NodeID,
			ParentID:  n.ParentID,
			Depth:     int64(n.Depth),
			Ord:       int64(n.Ord),
			Title:     n.Title,
			Summary:   n.Summary,
			StartPage: nullInt(n.StartPage),
			EndPage:   nullInt(n.EndPage),
			TextHash:  textHash,
		}); err != nil {
			return fmt.Errorf("insert node %s: %w", n.NodeID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit nodes: %w", err)
	}
	return nil
}

// ListSourceNodes returns a source's nodes in document order.
func (r *SourceRepository) ListSourceNodes(ctx context.Context, sourceID ulid.ULID) ([]entities.SourceNode, error) {
	rows, err := r.q.ListSourceNodes(ctx, sourceID[:])
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]entities.SourceNode, 0, len(rows))
	for _, row := range rows {
		n := entities.SourceNode{
			NodeID:   row.NodeID,
			ParentID: row.ParentID,
			Depth:    int(row.Depth),
			Ord:      int(row.Ord),
			Title:    row.Title,
			Summary:  row.Summary,
			// The DB keeps the text's HASH, never the text: "does this node carry text?" is what it
			// can answer, and that is what the outline needs.
			HasText: row.TextHash != "",
		}
		if row.StartPage.Valid {
			n.StartPage = int(row.StartPage.Int64)
		}
		if row.EndPage.Valid {
			n.EndPage = int(row.EndPage.Int64)
		}
		out = append(out, n)
	}
	return out, nil
}
