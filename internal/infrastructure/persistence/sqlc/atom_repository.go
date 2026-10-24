package sqlc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc/dal"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// idBatch bounds one `IN (…)` read. SQLite accepts a finite number of bound variables per statement
// (999 in some builds, 32766 in others); a cluster pass can touch tens of thousands of members at
// once, so the repository batches instead of sending the vault in one statement.
const idBatch = 500

// AtomRepository implements interfaces.AtomRepository.
type AtomRepository struct {
	db *sql.DB
	q  *dal.Queries
}

var _ interfaces.AtomRepository = (*AtomRepository)(nil)

// NewAtomRepository wraps a database handle.
func NewAtomRepository(db *sql.DB) *AtomRepository {
	return &AtomRepository{db: db, q: dal.New(db)}
}

// SaveAtom upserts an atom, its provenance and its links in one transaction.
func (r *AtomRepository) SaveAtom(ctx context.Context, a entities.Atom) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := r.q.WithTx(tx)
	id := a.ID[:]

	bodyHash := sha256.Sum256([]byte(a.Body))
	if err := q.UpsertAtom(ctx, dal.UpsertAtomParams{
		ID:          a.ID,
		Topic:       a.Topic,
		Path:        a.Path,
		Claim:       a.Claim,
		Body:        a.Body,
		Frontmatter: "{}",
		Type:        string(a.Type),
		Status:      string(a.Status),
		BodyHash:    hex.EncodeToString(bodyHash[:]),
		Size:        int64(len(a.Body)),
		Mtime:       a.UpdatedAt.UnixMilli(),
		CreatedAt:   a.CreatedAt.UnixMilli(),
		UpdatedAt:   a.UpdatedAt.UnixMilli(),
		IndexedAt:   a.UpdatedAt.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("upsert atom: %w", err)
	}

	// Resolve every source hash BEFORE the atom's rows are touched. A lookup that fails
	// must never wipe the provenance the atom already has: an atom without provenance is
	// invisible in its source's claim list and can never be attributed again — that is how
	// a source ends up showing zero claims while its pages still exist. It is an error, not
	// a silent skip.
	type provenance struct {
		sourceID ulid.ULID
		nodeID   string
		ocr      int64
	}
	refs := make([]provenance, 0, len(a.Sources))
	for _, s := range a.Sources {
		sourceID, err := q.GetSourceIDByHash(ctx, s.Hash)
		if err != nil {
			return types.New(
				"unknown_source",
				fmt.Sprintf("atom %s cites source %s, which is not in the index", a.ID.String(), s.Hash),
				types.ErrNotFound,
			)
		}
		refs = append(refs, provenance{sourceID: sourceID, nodeID: s.NodeID, ocr: boolInt(s.OCR)})
	}

	if err := q.DeleteAtomSources(ctx, a.ID); err != nil {
		return fmt.Errorf("clear sources: %w", err)
	}
	for _, ref := range refs {
		if err := q.InsertAtomSource(ctx, dal.InsertAtomSourceParams{
			AtomID: a.ID, SourceID: ref.sourceID, NodeID: ref.nodeID, Ocr: ref.ocr,
		}); err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
	}

	if err := q.DeleteAtomLinks(ctx, id); err != nil {
		return fmt.Errorf("clear links: %w", err)
	}
	for _, l := range a.Links {
		if err := q.InsertAtomLink(ctx, dal.InsertAtomLinkParams{
			FromID: id, RawTarget: l.RawTarget, ToID: linkTarget(l.ToID),
			Alias: l.Alias, Heading: l.Heading, Position: int64(l.Position),
		}); err != nil {
			return fmt.Errorf("insert link: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit atom: %w", err)
	}
	return nil
}

// GetAtom returns an atom with its provenance.
func (r *AtomRepository) GetAtom(ctx context.Context, id ulid.ULID) (entities.Atom, error) {
	return r.getAtomHydrated(ctx, id)
}

// getAtomHydrated reads the row plus its provenance, so a single-atom read round-trips.
func (r *AtomRepository) getAtomHydrated(ctx context.Context, id ulid.ULID) (entities.Atom, error) {
	row, err := r.q.GetAtom(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return entities.Atom{}, types.New("not_found", "no such atom", types.ErrNotFound)
	}
	if err != nil {
		return entities.Atom{}, fmt.Errorf("get atom: %w", err)
	}
	a := toAtom(row)

	sources, err := r.q.ListAtomSources(ctx, id)
	if err != nil {
		return entities.Atom{}, fmt.Errorf("list sources: %w", err)
	}
	for _, s := range sources {
		a.Sources = append(a.Sources, entities.AtomSource{Hash: s.Hash, Title: s.Title, NodeID: s.NodeID, OCR: s.Ocr != 0})
	}
	a.SourceCount = len(a.Sources)
	// The links too: a SaveAtom is authoritative for them, so a read that handed back an atom without
	// its links would DELETE them on the next save. "Round-trips" means ALL of what a save replaces.
	linkRows, err := r.q.AtomLinksForAtoms(ctx, [][]byte{id[:]})
	if err != nil {
		return entities.Atom{}, fmt.Errorf("list links: %w", err)
	}
	for _, row := range linkRows {
		link := entities.Link{
			RawTarget: row.RawTarget, Alias: row.Alias, Heading: row.Heading, Position: int(row.Position),
		}
		if len(row.ToID) == len(ulid.ULID{}) {
			link.ToID = idFromBytes(row.ToID)
		}
		a.Links = append(a.Links, link)
	}
	return a, nil
}

// NodeRefsBySource returns, per atom of a source, the tree nodes the atom cites. It
// is how a re-derivation recognises which new page covers the material of a removed one.
func (r *AtomRepository) NodeRefsBySource(ctx context.Context, hash string) (map[ulid.ULID][]string, error) {
	rows, err := r.q.NodeRefsBySource(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("node refs by source: %w", err)
	}
	m := make(map[ulid.ULID][]string, len(rows))
	for _, row := range rows {
		m[row.AtomID] = append(m[row.AtomID], row.NodeID)
	}
	return m, nil
}

// hydrate fills in what a SAVE replaces and a row does not carry: the tags, the PROVENANCE and the
// links. It is the rule for every list read — a caller that re-saves what a read handed it (a rename, a
// repair) would otherwise silently DELETE the parts the read left out. Provenance is the worst of them:
// an atom without it is invisible in its source's claim list, the source looks underived, and the next
// sweep derives the whole document again.
//
// It asks for the pages rows, never for the table. The whole-table version read 170k provenance rows
// to decorate a page of 50 claims — both the second of latency the list view showed and the hundreds
// of megabytes the UI polling left behind on every refresh.
//
// A failing read is an ERROR here, not a missing field. The rule hydrate exists for is that a caller
// may re-save what it handed back; handing back an atom whose provenance query failed would delete
// that provenance on the next save, quietly.
func (r *AtomRepository) hydrate(ctx context.Context, out []entities.Atom) ([]entities.Atom, error) {
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]ulid.ULID, 0, len(out))
	raw := make([][]byte, 0, len(out))
	for i := range out {
		ids = append(ids, out[i].ID)
		raw = append(raw, out[i].ID[:])
	}
	rows, err := r.q.AtomSourcesForAtoms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate: provenance: %w", err)
	}
	byAtom := make(map[ulid.ULID][]entities.AtomSource, len(rows))
	for _, row := range rows {
		byAtom[row.AtomID] = append(byAtom[row.AtomID], entities.AtomSource{
			Hash: row.Hash, NodeID: row.NodeID, OCR: row.Ocr != 0,
		})
	}
	links, err := r.q.AtomLinksForAtoms(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("hydrate: links: %w", err)
	}
	byFrom := make(map[ulid.ULID][]entities.Link, len(links))
	for _, row := range links {
		link := entities.Link{
			RawTarget: row.RawTarget, Alias: row.Alias, Heading: row.Heading, Position: int(row.Position),
		}
		from := idFromBytes(row.FromID)
		if len(row.ToID) == len(ulid.ULID{}) {
			link.ToID = idFromBytes(row.ToID)
		}
		byFrom[from] = append(byFrom[from], link)
	}
	for i := range out {
		out[i].Sources = byAtom[out[i].ID]
		out[i].Links = byFrom[out[i].ID]
	}
	counts, err := r.q.AtomSourceCountsForAtoms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate: source counts: %w", err)
	}
	n := make(map[ulid.ULID]int, len(counts))
	for _, c := range counts {
		n[c.AtomID] = int(c.Count)
	}
	for i := range out {
		out[i].SourceCount = n[out[i].ID]
	}
	return out, nil
}

// ResolveLinks resolves atom→atom wikilinks whose raw target is the claimed sentence. It is the
// atom layer's only link resolution: the claim graph is the graph, and the claim
// graph was not, so `[[Claim Text]]` stayed red forever.
func (r *AtomRepository) ResolveLinks(ctx context.Context) (int64, error) {
	n, err := r.q.ResolveAtomLinks(ctx)
	if err != nil {
		return 0, fmt.Errorf("resolve atom links: %w", err)
	}
	return n, nil
}

// UnresolvedLinks lists atom links whose target does not exist: the lint finding for a claim that
// points at a sentence nobody wrote.
func (r *AtomRepository) UnresolvedLinks(ctx context.Context, limit int) ([]entities.UnresolvedLink, error) {
	rows, err := r.q.ListUnresolvedAtomLinks(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("unresolved atom links: %w", err)
	}
	out := make([]entities.UnresolvedLink, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.UnresolvedLink{
			FromID:    idFromBytes(row.FromID),
			FromTitle: row.FromTitle,
			RawTarget: row.RawTarget,
		})
	}
	return out, nil
}

// ListAtoms lists atoms, optionally filtered by topic.
func (r *AtomRepository) ListAtoms(ctx context.Context, topic string, limit, offset int) ([]entities.Atom, error) {
	var (
		rows []dal.Atom
		err  error
	)
	if topic == "" {
		rows, err = r.q.ListAtoms(ctx, dal.ListAtomsParams{Limit: int64(limit), Offset: int64(offset)})
	} else {
		rows, err = r.q.ListAtomsByTopic(ctx, dal.ListAtomsByTopicParams{Topic: topic, Limit: int64(limit), Offset: int64(offset)})
	}
	if err != nil {
		return nil, fmt.Errorf("list atoms: %w", err)
	}
	out := make([]entities.Atom, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAtom(row))
	}
	return r.hydrate(ctx, out)
}

// ListAtomsBySource returns the atoms whose provenance points at a source.
func (r *AtomRepository) ListAtomsBySource(ctx context.Context, sourceHash string) ([]entities.Atom, error) {
	rows, err := r.q.ListAtomsBySource(ctx, sourceHash)
	if err != nil {
		return nil, fmt.Errorf("list atoms by source: %w", err)
	}
	out := make([]entities.Atom, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAtom(row))
	}
	return out, nil
}

// ListOldestAtoms returns the oldest claims in (updated_at, id) order, up to limit: the queue a
// time-bounded repair walks. No provenance, no links, no counts — and no whole-table read: the index
// answers the order, so a budget of 12k claims costs 12k rows and nothing else.
func (r *AtomRepository) ListOldestAtoms(ctx context.Context, limit int) ([]entities.Atom, error) {
	rows, err := r.q.ListAtomsOldest(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list oldest atoms: %w", err)
	}
	out := make([]entities.Atom, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.Atom{
			ID: row.ID, Topic: row.Topic, Claim: row.Claim, Body: row.Body,
			UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		})
	}
	return out, nil
}

// ListTopics returns claim topics with atom counts.
func (r *AtomRepository) ListClaimTopics(ctx context.Context) ([]entities.TopicUse, error) {
	rows, err := r.q.ListClaimTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list topics: %w", err)
	}
	out := make([]entities.TopicUse, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.TopicUse{Topic: row.Topic, Count: int(row.Count)})
	}
	return out, nil
}

// StreamAtomTexts walks every claim's text in id order, one keyset page at a time, handing each
// claim to fn and dropping it. The association pass folds the whole-vault statistics (document
// frequency, cluster centroid sums) from this stream: materialising the corpus read 170k claim
// texts per pass, when the pass itself keeps almost nothing. fn keeps nothing it is handed.
func (r *AtomRepository) StreamAtomTexts(ctx context.Context, fn func(entities.Atom) error) error {
	const page = 2000
	var after ulid.ULID // the zero id: ids are BLOBs, and every real claim compares greater
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := r.q.ListAtomTextsPage(ctx, dal.ListAtomTextsPageParams{ID: after, Limit: page})
		if err != nil {
			return fmt.Errorf("stream atoms: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := fn(entities.Atom{ID: row.ID, Topic: row.Topic, Claim: row.Claim, Body: row.Body}); err != nil {
				return err
			}
		}
		if len(rows) < page {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// StreamClaimIdentities walks every claim's identity in id order, one keyset page at a time: the
// fields that make a claim a claim, plus its provenance (one scoped query per page). It exists for
// the duplicate check, which used to decode 170k atom FILES to answer a 200-byte question.
func (r *AtomRepository) StreamClaimIdentities(ctx context.Context, fn func(entities.Atom) error) error {
	const page = 2000
	var after ulid.ULID
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := r.q.ListAtomIdentityPage(ctx, dal.ListAtomIdentityPageParams{ID: after, Limit: page})
		if err != nil {
			return fmt.Errorf("stream claim identities: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]ulid.ULID, 0, len(rows))
		atoms := make([]entities.Atom, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			atoms = append(atoms, entities.Atom{
				ID: row.ID, Topic: row.Topic, Claim: row.Claim, Body: row.Body,
				Type: entities.AtomType(row.Type), Status: entities.AtomStatus(row.Status),
			})
		}
		prov, err := r.q.AtomSourcesForAtoms(ctx, ids)
		if err != nil {
			return fmt.Errorf("stream claim identities: provenance: %w", err)
		}
		byAtom := make(map[ulid.ULID][]entities.AtomSource, len(ids))
		for _, row := range prov {
			byAtom[row.AtomID] = append(byAtom[row.AtomID], entities.AtomSource{
				Hash: row.Hash, NodeID: row.NodeID, OCR: row.Ocr != 0,
			})
		}
		for i := range atoms {
			atoms[i].Sources = byAtom[atoms[i].ID]
			if err := fn(atoms[i]); err != nil {
				return err
			}
		}
		if len(rows) < page {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// AtomTextsForIDs returns the text of exactly these claims, in id order.
//
// The ids are read in batches: a cluster pass asks for the members of every cluster it touched, and
// one IN (…) with tens of thousands of variables is a SQL error, not a big query.
func (r *AtomRepository) AtomTextsForIDs(ctx context.Context, ids []ulid.ULID) ([]entities.Atom, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]entities.Atom, 0, len(ids))
	for start := 0; start < len(ids); start += idBatch {
		end := min(start+idBatch, len(ids))
		rows, err := r.q.AtomTextsForIDs(ctx, ids[start:end])
		if err != nil {
			return nil, fmt.Errorf("atom texts: %w", err)
		}
		for _, row := range rows {
			out = append(out, entities.Atom{ID: row.ID, Topic: row.Topic, Claim: row.Claim, Body: row.Body})
		}
	}
	// The single-statement query promises id order; the batches are not globally ordered.
	slices.SortFunc(out, func(a, b entities.Atom) int { return a.ID.Compare(b.ID) })
	return out, nil
}

// AtomCount is the claim total behind a progress line — a count, never the claims.
func (r *AtomRepository) AtomCount(ctx context.Context) (int, error) {
	n, err := r.q.CountAtoms(ctx)
	if err != nil {
		return 0, fmt.Errorf("count atoms: %w", err)
	}
	return int(n), nil
}

// SourcesForAtoms returns the provenance of exactly these claims, keyed by claim id.
func (r *AtomRepository) SourcesForAtoms(ctx context.Context, ids []ulid.ULID) (map[ulid.ULID][]entities.AtomSource, error) {
	if len(ids) == 0 {
		return map[ulid.ULID][]entities.AtomSource{}, nil
	}
	byAtom := make(map[ulid.ULID][]entities.AtomSource, len(ids))
	for start := 0; start < len(ids); start += idBatch {
		end := min(start+idBatch, len(ids))
		rows, err := r.q.AtomSourcesForAtoms(ctx, ids[start:end])
		if err != nil {
			return nil, fmt.Errorf("sources for atoms: %w", err)
		}
		for _, row := range rows {
			byAtom[row.AtomID] = append(byAtom[row.AtomID], entities.AtomSource{
				Hash: row.Hash, NodeID: row.NodeID, OCR: row.Ocr != 0,
			})
		}
	}
	return byAtom, nil
}

// AtomIDs returns the id of every claim — the existence set a consistency check compares the cluster
// membership against. It reads no body: the check asks whether a claim is there, never what it says.
func (r *AtomRepository) AtomIDs(ctx context.Context) ([]ulid.ULID, error) {
	ids, err := r.q.AllAtomIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("atom ids: %w", err)
	}
	return ids, nil
}

// AtomAddresses returns the id and the recorded file path of every claim, so a stale row can remove
// the FILE it points at. A use case must not learn the store's folder convention to clean up.
func (r *AtomRepository) AtomAddresses(ctx context.Context) ([]entities.Atom, error) {
	rows, err := r.q.AllAtomAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("atom addresses: %w", err)
	}
	out := make([]entities.Atom, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.Atom{ID: row.ID, Path: row.Path})
	}
	return out, nil
}

// SupersedeAtomsBySource marks every atom citing that source as superseded. It is
// called when a URL's content changes, so the page's old claims are visibly stale.
func (r *AtomRepository) SupersedeAtomsBySource(ctx context.Context, sourceHash string) (int64, error) {
	n, err := r.q.SupersedeAtomsBySource(ctx, dal.SupersedeAtomsBySourceParams{
		UpdatedAt: time.Now().UnixMilli(),
		Hash:      sourceHash,
	})
	if err != nil {
		return 0, fmt.Errorf("supersede atoms by source: %w", err)
	}
	return n, nil
}

// DeleteAtom removes an atom row (links, provenance and note links cascade).
func (r *AtomRepository) DeleteAtom(ctx context.Context, id ulid.ULID) error {
	if err := r.q.DeleteAtom(ctx, id); err != nil {
		return fmt.Errorf("delete atom: %w", err)
	}
	return nil
}

// linkTarget turns a domain id into the nullable BLOB a link stores: a zero id stays NULL (a red link).
func linkTarget(id ulid.ULID) []byte {
	if id == (ulid.ULID{}) {
		return nil
	}
	return []byte(id[:])
}

// CountAtomSources returns how many sources an atom cites.
func (r *AtomRepository) CountAtomSources(ctx context.Context, id ulid.ULID) (int, error) {
	n, err := r.q.CountAtomSources(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count atom sources: %w", err)
	}
	return int(n), nil
}

func toAtom(row dal.Atom) entities.Atom {
	return entities.Atom{
		ID:        row.ID,
		Topic:     row.Topic,
		Path:      row.Path,
		Claim:     row.Claim,
		Body:      row.Body,
		Type:      entities.AtomType(row.Type),
		Status:    entities.AtomStatus(row.Status),
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(),
		UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
