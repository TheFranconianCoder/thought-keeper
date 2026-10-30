package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// CommandRunner reads one utterance and returns the command it became. The context names the
// surface the reader made the request from ("this claim" needs to know which one).
type CommandRunner interface {
	Execute(ctx context.Context, text string, context usecases.CommandRefs) (partialentities.PartialCommand, error)
}

// CommandLister returns the assistant's history.
type CommandLister interface {
	Execute(ctx context.Context, limit, offset int) ([]partialentities.PartialCommand, error)
}

// CommandGetter returns one command.
type CommandGetter interface {
	Execute(ctx context.Context, id ulid.ULID) (partialentities.PartialCommand, error)
}

// CommandDeleter removes one command from the history.
type CommandDeleter interface {
	Execute(ctx context.Context, id ulid.ULID) error
}

// CommandNotifier broadcasts a command to every connected client. It is what makes the assistant a
// REMOTE control: the request may come from anywhere, and the UI that is open follows the address.
type CommandNotifier interface {
	Publish(typ string, data any)
}

// CommandHandler serves the assistant's commands.
type CommandHandler struct {
	Runner   CommandRunner
	Lister   CommandLister
	Getter   CommandGetter
	Deleter  CommandDeleter
	Notifier CommandNotifier
}

// Post serves POST /api/v1/commands.
//
// It is synchronous on purpose: a person who just asked for a list is waiting for the list, and the
// answer is a bounded completion plus an index read. The "running" frame goes out FIRST, so a client
// that did not send the request still shows that the assistant is working.
func (h CommandHandler) Post(w http.ResponseWriter, r *http.Request) {
	var req payloads.CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		payloads.WriteError(w, types.New("invalid_body", "invalid JSON body", err))
		return
	}
	if h.Notifier != nil {
		h.Notifier.Publish("command", map[string]any{"status": "running", "text": req.Text})
	}
	refs := usecases.CommandRefs{}
	if req.Context != nil {
		refs.View, refs.ID = req.Context.View, req.Context.ID
	}
	cmd, err := h.Runner.Execute(r.Context(), req.Text, refs)
	if err != nil {
		if h.Notifier != nil {
			h.Notifier.Publish("command", map[string]any{
				"status": "failed", "text": req.Text, "error": err.Error(), "code": types.CodeOf(err),
			})
		}
		payloads.WriteError(w, err)
		return
	}
	out := commandPayload(cmd, true)
	if h.Notifier != nil {
		h.Notifier.Publish("command", map[string]any{
			"status": "done", "id": out.ID, "url": out.URL, "lens": out.Lens,
			"total": out.Total, "text": out.Text,
		})
	}
	payloads.WriteJSON(w, http.StatusCreated, out)
}

// List serves GET /api/v1/commands.
func (h CommandHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Lister.Execute(r.Context(), atoiDefault(r.URL.Query().Get("limit"), 20), atoiDefault(r.URL.Query().Get("offset"), 0))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	out := make([]payloads.Command, 0, len(items))
	for _, c := range items {
		out = append(out, commandPayload(c, false))
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.CommandList{Commands: out, Count: len(out)})
}

// GetOne serves GET /api/v1/commands/{id}.
func (h CommandHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	cmd, err := h.Getter.Execute(r.Context(), id)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, commandPayload(cmd, true))
}

// DeleteOne serves DELETE /api/v1/commands/{id}.
func (h CommandHandler) DeleteOne(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.ParseStrict(chi.URLParam(r, "id"))
	if err != nil {
		payloads.WriteError(w, types.New("invalid_id", "id must be a 26-character ULID", types.ErrInvalidID))
		return
	}
	if err := h.Deleter.Execute(r.Context(), id); err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

func commandPayload(c partialentities.PartialCommand, withItems bool) payloads.Command {
	out := payloads.Command{
		ID: c.ID.String(), Text: c.Text, Lens: c.Lens, URL: c.URL,
		Resolution: c.Resolution, Rationale: c.Rationale, Notes: c.Notes,
		Total: c.Total, Vault: c.Vault, Scanned: c.Scanned, Generated: c.Generated,
		TookMs:    c.Took.Milliseconds(),
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		Criteria: payloads.CommandCriteria{
			Text: c.Criteria.Text, Tag: c.Criteria.Tag, Kind: c.Criteria.Kind,
			Topic: c.Criteria.Topic, FromTopic: c.Criteria.FromTopic,
			Tags: c.Criteria.Tags, Topics: c.Criteria.Topics,
			Source: c.Criteria.Source, Atom: c.Criteria.Atom,
			Since: durationField(c.Criteria.Since),
			State: c.Criteria.State, Order: c.Criteria.Order,
			BookmarksOnly: c.Criteria.BookmarksOnly, AttachedOnly: c.Criteria.AttachedOnly,
		},
	}
	if len(c.Mapped) > 0 {
		out.Mapped = make([]payloads.CommandName, 0, len(c.Mapped))
		for _, m := range c.Mapped {
			out.Mapped = append(out.Mapped, payloads.CommandName{Name: m.Name, Kind: m.Kind, Count: m.Count, Found: m.Found})
		}
	}
	if withItems {
		out.Items = make([]payloads.CommandItem, 0, len(c.Items))
		for _, it := range c.Items {
			out.Items = append(out.Items, payloads.CommandItem{Ref: it.Ref, Title: it.Title, Kind: it.Kind, Why: it.Why})
		}
	}
	return out
}

// durationField writes a window as the same shape a config uses ("168h"), or empty for "no window".
func durationField(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
