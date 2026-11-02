package payloads

// CommandRequest is the body of POST /api/v1/commands: one utterance, verbatim, plus what the
// reader is looking at — so "this claim" has a meaning.
type CommandRequest struct {
	Text    string          `json:"text"`
	Context *CommandContext `json:"context,omitempty"`
}

// CommandContext is the surface the request was made from. The server decides what the id IS (a
// source hash or a claim id); the client only says where the reader stands.
type CommandContext struct {
	View string `json:"view,omitempty"`
	ID   string `json:"id,omitempty"`
	// Timezone is the reader's IANA timezone (Intl.DateTimeFormat().resolvedOptions().timeZone).
	// It decides what "yesterday" and "this week" mean for the command's window.
	Timezone string `json:"timezone,omitempty"`
}

// CommandWindow is a command's time window: the reader's phrase family, the instants it resolved to
// for this run, and the timezone that decided the calendar.
type CommandWindow struct {
	Spec     string `json:"spec,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// CommandCriteria is the interpretation the command was resolved from. The banner shows it, so a
// reader can tell a good reading from a bad one instead of trusting a list.
type CommandCriteria struct {
	Text          string         `json:"text,omitempty"`
	Terms         []string       `json:"terms,omitempty"`
	Tag           string         `json:"tag,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Topic         string         `json:"topic,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Topics        []string       `json:"topics,omitempty"`
	FromTopic     string         `json:"from_topic,omitempty"`
	Source        string         `json:"source,omitempty"`
	Atom          string         `json:"atom,omitempty"`
	Window        *CommandWindow `json:"window,omitempty"`
	State         string         `json:"state,omitempty"`
	Order         string         `json:"order,omitempty"`
	BookmarksOnly bool           `json:"bookmarks_only,omitempty"`
	AttachedOnly  bool           `json:"attached_only,omitempty"`
}

// CommandName is one mapped vocabulary name and what it contributed.
type CommandName struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	Found int    `json:"found"`
}

// CommandItem is one member of the materialized set.
type CommandItem struct {
	Ref   string `json:"ref"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Why   string `json:"why,omitempty"`
}

// Command is one "request → GUI state" record.
type Command struct {
	ID         string          `json:"id"`
	Text       string          `json:"text"`
	Lens       string          `json:"lens"`
	URL        string          `json:"url"`
	Resolution string          `json:"resolution,omitempty"`
	Rationale  string          `json:"rationale,omitempty"`
	Notes      []string        `json:"notes,omitempty"`
	Total      int             `json:"total"`
	Vault      int             `json:"vault,omitempty"`
	Scanned    int             `json:"scanned,omitempty"`
	Generated  string          `json:"generated,omitempty"`
	TookMs     int64           `json:"took_ms"`
	CreatedAt  string          `json:"created_at"`
	Criteria   CommandCriteria `json:"criteria"`
	Mapped     []CommandName   `json:"mapped,omitempty"`
	Items      []CommandItem   `json:"items,omitempty"`
}

// CommandList is the response of GET /api/v1/commands.
type CommandList struct {
	Commands []Command `json:"commands"`
	Count    int       `json:"count"`
}

// CommandRef is what a LIST response says about the command it is showing: whether the set came
// from a record, and whether that record has already been pruned. Without it a lens cannot tell
// "the assistant's result" from "a filtered list", and an expired address would look like a normal
// unfiltered one.
type CommandRef struct {
	ID      string `json:"id"`
	Expired bool   `json:"expired,omitempty"`
	Total   int    `json:"total"`
	Missing int    `json:"missing,omitempty"`
}
