package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// AtomLister lists atoms.
type AtomLister interface {
	Execute(ctx context.Context, topic string, limit, offset int) ([]partialentities.PartialAtom, error)
}

// ClaimTopicLister lists claim topics.
type ClaimTopicLister interface {
	Execute(ctx context.Context) ([]partialentities.PartialBranch, error)
}

// AtomGetter returns one atom.
type AtomGetter interface {
	Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialAtom, error)
}

// AtomRelated returns claims related to one atom, grouped by relation.
type AtomRelated interface {
	Execute(ctx context.Context, id ulid.ULID, limit int) (usecases.RelatedAtomsResult, error)
}

// URLKindFixer repairs sources whose index entry lost their kind.
type URLKindFixer interface {
	Execute(ctx context.Context) (usecases.FixURLsResult, error)
}

// SourceAtomLister lists the atoms extracted from one source.
type SourceAtomLister interface {
	Execute(ctx context.Context, hash string, limit int) (usecases.SourceAtomsResult, error)
}

// RunGetter returns one run.
type RunGetter interface {
	Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialRun, error)
}

// AtomLayoutReader reports the OLD claim layout a vault still holds (branch folders). Read-only.
type AtomLayoutReader interface {
	Detect(ctx context.Context) (partialentities.PartialAtomLayout, error)
}

// AtomLayoutWriter moves the claims to the layout that addresses them by their ID.
type AtomLayoutWriter interface {
	Execute(ctx context.Context) (partialentities.PartialAtomLayout, error)
}

// AtomDuplicateReader reports claims that exist more than once. Read-only.
type AtomDuplicateReader interface {
	Detect(ctx context.Context) (partialentities.PartialAtomDuplicates, error)
}

// AtomDuplicateCollapser keeps one copy of each claim and trashes the rest.
type AtomDuplicateCollapser interface {
	Execute(ctx context.Context) (partialentities.PartialAtomDuplicates, error)
}

// ClaimCodeQuoter wraps bracket syntax in claim bodies in an inline code mark.
type ClaimCodeQuoter interface {
	Execute(ctx context.Context) (usecases.QuoteClaimCodeResult, error)
}

// AtomHandler serves the atom, claim-topic and run endpoints.
type AtomHandler struct {
	Atoms       AtomLister
	ClaimTopics ClaimTopicLister
	GetAtom     AtomGetter
	AtomRelated AtomRelated
	SourceAtoms SourceAtomLister
	Runs        RunGetter
	// AtomLayout is what a vault still holds in the old layout; AtomReorganize moves it.
	AtomLayout     AtomLayoutReader
	AtomReorganize AtomLayoutWriter
	// AtomDuplicates is what exists more than once; AtomCollapse keeps one copy of each claim.
	AtomDuplicates AtomDuplicateReader
	AtomCollapse   AtomDuplicateCollapser
	// QuoteCode wraps bracket syntax in claim bodies in inline code (a one-time repair).
	QuoteCode ClaimCodeQuoter
	// Mark shows a synchronous pass in the Running list while it works.
	Mark MaintenanceMarker
	// Commands resolves an assistant result (?command=<id>) into claims. Nil means the address is
	// not understood here; the named criteria beside it still filter.
	Commands CommandAtomSet
}

// CommandAtomSet is an assistant result's materialized claim set, in the command's order.
type CommandAtomSet interface {
	Execute(ctx context.Context, id ulid.ULID, limit, offset int) ([]partialentities.PartialAtom, usecases.CommandWindow, error)
}

// GetAtomLayout serves GET /api/v1/atoms/layout: the legacy topic folders a vault written before the ID
// layout still holds, so a reader can see whether the reorganize has anything to do.
func (h AtomHandler) GetAtomLayout(w http.ResponseWriter, r *http.Request) {
	if h.AtomLayout == nil {
		payloads.WriteJSON(w, http.StatusOK, payloads.AtomLayout{})
		return
	}
	res, err := h.AtomLayout.Detect(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, atomLayoutFromPartial(res))
}

// PostAtomReorganize serves POST /api/v1/atoms/reorganize: move every claim out of the legacy topic folders
// to its canonical path and remove the folders that empty out. Idempotent.
func (h AtomHandler) PostAtomReorganize(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("reorganize-atoms", "")()
	}
	if h.AtomReorganize == nil {
		payloads.WriteError(w, types.New("not_implemented", "reorganize is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.AtomReorganize.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, atomLayoutFromPartial(res))
}

// GetAtomDuplicates serves GET /api/v1/atoms/duplicates: the claims that exist more than once, so a
// reader sees whether a collapse has anything to do.
func (h AtomHandler) GetAtomDuplicates(w http.ResponseWriter, r *http.Request) {
	if h.AtomDuplicates == nil {
		payloads.WriteJSON(w, http.StatusOK, payloads.AtomDuplicates{})
		return
	}
	res, err := h.AtomDuplicates.Detect(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, atomDuplicatesFromPartial(res))
}

// PostAtomCollapse serves POST /api/v1/atoms/collapse: keep one copy of every claim that exists more
// than once and move the rest to the vault's .trash/atoms/. Idempotent.
func (h AtomHandler) PostAtomCollapse(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("collapse-atoms", "")()
	}
	if h.AtomCollapse == nil {
		payloads.WriteError(w, types.New("not_implemented", "collapse is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.AtomCollapse.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, atomDuplicatesFromPartial(res))
}

// PostQuoteCode serves POST /api/v1/atoms/quote-code: wrap the bracket syntax a claim body quotes
// from its source in inline code. Idempotent, and it changes no claim, topic or provenance.
func (h AtomHandler) PostQuoteCode(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("quote-claim-code", "")()
	}
	if h.QuoteCode == nil {
		payloads.WriteError(w, types.New("not_implemented", "quote claim code is not configured", types.ErrNotImplemented))
		return
	}
	res, err := h.QuoteCode.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.ClaimCode{
		Scanned: res.Scanned, Repaired: res.Repaired, Fragments: res.Fragments, Skipped: res.Skipped,
	})
}

func atomDuplicatesFromPartial(p partialentities.PartialAtomDuplicates) payloads.AtomDuplicates {
	return payloads.AtomDuplicates{Groups: p.Groups, Files: p.Files, Collapsed: p.Collapsed, Example: p.Example}
}

func atomLayoutFromPartial(p partialentities.PartialAtomLayout) payloads.AtomLayout {
	return payloads.AtomLayout{
		Folders: p.Folders, Claims: p.Claims, Indexes: p.Indexes,
		Moved: p.Moved, Removed: p.Removed, IndexesRemoved: p.IndexesRemoved,
		Skipped: p.Skipped, Kept: p.Kept,
	}
}

// ListSourceAtoms serves GET /api/v1/sources/{hash}/atoms.
func (h AtomHandler) ListSourceAtoms(w http.ResponseWriter, r *http.Request) {
	if h.SourceAtoms == nil {
		payloads.WriteJSON(w, http.StatusOK, payloads.AtomList{Atoms: []payloads.Atom{}, Count: 0})
		return
	}
	// Bounded on purpose: a book has hundreds of claims, and shipping every body is what made the
	// overlay feel slow. The total and the distinct topics come along, so the rail still shows
	// the whole picture.
	limit := min(atoiDefault(r.URL.Query().Get("limit"), usecases.DefaultSourceAtomsLimit), 500)
	res, err := h.SourceAtoms.Execute(r.Context(), chi.URLParam(r, "hash"), limit)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	topics := make([]payloads.SourceTopic, 0, len(res.Topics))
	for _, t := range res.Topics {
		topics = append(topics, payloads.SourceTopic{Name: t.Name, Count: t.Count})
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.AtomList{
		Atoms: payloads.AtomsFromPartials(res.Atoms), Count: len(res.Atoms),
		Total: res.Total, Topics: topics,
	})
}

// ListAtoms serves GET /api/v1/atoms.
func (h AtomHandler) ListAtoms(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 200)
	offset := atoiDefault(q.Get("offset"), 0)
	// An assistant result is read from its record: the set is what the command found, not what the
	// current filter would find.
	handled, expired := h.serveCommand(w, r, limit, offset)
	if handled {
		return
	}
	atoms, err := h.Atoms.Execute(r.Context(), q.Get("topic"), limit, offset)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.AtomList{
		Atoms: payloads.AtomsFromPartials(atoms), Count: len(atoms), Command: expired,
	})
}

// serveCommand serves an assistant result's claim set and reports whether it wrote the response
// (and, for a pruned record, the ref that marks the address as expired).
func (h AtomHandler) serveCommand(w http.ResponseWriter, r *http.Request, limit, offset int) (bool, *payloads.CommandRef) {
	raw := strings.TrimSpace(r.URL.Query().Get("command"))
	if raw == "" || h.Commands == nil {
		return false, nil
	}
	id, err := ulid.ParseStrict(raw)
	if err != nil {
		return false, nil
	}
	atoms, window, cErr := h.Commands.Execute(r.Context(), id, limit, offset)
	if cErr != nil {
		if types.CodeOf(cErr) == "not_found" {
			return false, &payloads.CommandRef{ID: id.String(), Expired: true}
		}
		return false, nil
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.AtomList{
		Atoms: payloads.AtomsFromPartials(atoms), Count: window.Total, Total: window.Total,
		Command: &payloads.CommandRef{ID: id.String(), Total: window.Total, Missing: window.Missing},
	})
	return true, nil
}

// ListClaimTopics serves GET /api/v1/topics/claims.
func (h AtomHandler) ListClaimTopics(w http.ResponseWriter, r *http.Request) {
	branches, err := h.ClaimTopics.Execute(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.TopicUseList{Topics: payloads.TopicUsesFromPartials(branches), Count: len(branches)})
}

// Get serves GET /api/v1/atoms/{id}.
// GetRelated serves GET /api/v1/atoms/{id}/related: claims near one claim (cluster first, topic next).
func (h AtomHandler) GetRelated(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	if h.AtomRelated == nil {
		payloads.WriteJSON(w, http.StatusOK, payloads.AtomRelated{
			Peers: []payloads.Atom{}, SameTopic: []payloads.Atom{}, Count: 0,
		})
		return
	}
	related, err := h.AtomRelated.Execute(r.Context(), id, atoiDefault(r.URL.Query().Get("limit"), 20))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.AtomRelated{
		Peers:     payloads.AtomsFromPartials(related.Peers),
		SameTopic: payloads.AtomsFromPartials(related.SameTopic),
		Count:     len(related.Peers) + len(related.SameTopic),
	})
}

func (h AtomHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	atom, err := h.GetAtom.Execute(r.Context(), id)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.AtomFromPartial(atom))
}

// GetRun serves GET /api/v1/runs/{id}.
func (h AtomHandler) GetRun(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	run, err := h.Runs.Execute(r.Context(), id)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.RunFromPartial(run))
}
