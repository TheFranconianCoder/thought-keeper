package entities

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// CommandLens is the lens a command lands on: which part of the UI the answer belongs to.
// A command has exactly one lens — a list has one row grammar and one count, and a command that
// mixed sources and claims could be neither.
type CommandLens string

const (
	CommandSources   CommandLens = "sources"
	CommandClaims    CommandLens = "claims"
	CommandWiki      CommandLens = "wiki"
	CommandStatus    CommandLens = "status"
	CommandAttention CommandLens = "attention"
	CommandAsk       CommandLens = "ask"
)

// Valid reports whether l names one of the lenses a command may address.
func (l CommandLens) Valid() bool {
	switch l {
	case CommandSources, CommandClaims, CommandWiki, CommandStatus, CommandAttention, CommandAsk:
		return true
	}
	return false
}

// CommandOrder is the order of a command's materialized set. Relevance is a RESULT, not a criterion:
// a lexical ranking cannot be re-derived later, which is why a command keeps its members.
type CommandOrder string

const (
	CommandOrderNewest    CommandOrder = "newest"
	CommandOrderOldest    CommandOrder = "oldest"
	CommandOrderTitle     CommandOrder = "title"
	CommandOrderRelevance CommandOrder = "relevance"
)

// CommandState is the derivation state a command may ask for. It is the one dimension the Sources
// lens has no control for: "which sources are still not distilled?" is a question about the step
// record, not about a tag.
type CommandState string

const (
	CommandStateAny    CommandState = ""
	CommandStateOpen   CommandState = "open"   // the derivation is not finished (pending or running)
	CommandStateFailed CommandState = "failed" // a step failed
	CommandStateDone   CommandState = "done"   // every document step is ok or skipped
)

// CommandCriteria is what a command MEANT: the criteria the intent was resolved to. It travels with
// the record so a reader can see the interpretation, and it is what "run again" re-resolves. The
// members below it are the answer; the criteria are why.
type CommandCriteria struct {
	// Text is the free text of a lexical search. It is NEVER written into a URL: an address that
	// carries the words of a question is a different address every time it is opened.
	Text string
	// Tag matches one human or derived source tag.
	Tag string
	// Kind is one source kind (md, pdf, txt, url).
	Kind string
	// Topic is one flat claim topic.
	Topic string
	// Tags and Topics are the NAMES the request's words were mapped onto: the vault's own semantic
	// layer (the tags an LLM put on sources, the topics it gave claims). A request about a subject
	// is answered by these names, never by reading content.
	Tags   []string
	Topics []string
	// FromTopic is a topic read as PROVENANCE: the sources that own claims with it. "Sources about
	// 37signals" is not a tag and not a substring — it is a question about what the claims say.
	FromTopic string
	// Source and Atom are EXPLICIT references: the reader pointed at one document or one claim (by
	// pasting its id, or by saying "this source" while looking at it). They narrow the set exactly —
	// the one source, the sources behind that claim, that claim, or that source's claims.
	Source string
	Atom   string
	// Since narrows sources to those that ARRIVED in the vault in the window (never publication).
	Since time.Duration
	// State narrows sources by their derivation's state.
	State CommandState
	// Order is the order of the materialized set.
	Order CommandOrder
	// BookmarksOnly keeps collected links; AttachedOnly keeps human-supplied snapshots.
	BookmarksOnly bool
	AttachedOnly  bool
}

// IsEmpty reports whether the criteria narrow nothing at all, which is a command that could not be
// understood rather than a command about the whole vault.
func (c CommandCriteria) IsEmpty() bool {
	return c.Text == "" && c.Tag == "" && c.Kind == "" && c.Topic == "" && c.FromTopic == "" &&
		len(c.Tags) == 0 && len(c.Topics) == 0 &&
		c.Source == "" && c.Atom == "" &&
		c.Since == 0 && c.State == CommandStateAny && !c.BookmarksOnly && !c.AttachedOnly
}

// NarrowsSources reports whether this reading says anything about a SOURCE list. A command whose
// criteria address only claims is not a narrowed source list — it is a request that was read into
// the wrong shape, and answering it with the whole vault is the one thing this feature must never
// do.
func (c CommandCriteria) NarrowsSources() bool {
	return c.Text != "" || c.Tag != "" || c.Kind != "" || c.FromTopic != "" ||
		len(c.Tags) > 0 || len(c.Topics) > 0 || c.Source != "" || c.Atom != "" ||
		c.Since > 0 || c.State != CommandStateAny || c.BookmarksOnly || c.AttachedOnly
}

// NarrowsClaims reports whether this reading says anything about a CLAIM list.
func (c CommandCriteria) NarrowsClaims() bool {
	return c.Text != "" || c.Topic != "" || c.Source != "" || c.Atom != "" || len(c.Topics) > 0
}

// NarrowsWiki reports whether this reading says anything about the reader's OWN pages. A wiki page
// has a title, tags and prose — text and tag are its only filters.
func (c CommandCriteria) NarrowsWiki() bool {
	return c.Text != "" || c.Tag != "" || len(c.Tags) > 0
}

// NarrowsAnything reports whether ANY lens could honour this reading. It is the last line of
// defence: a request whose criteria nobody can apply is not a command.
func (c CommandCriteria) NarrowsAnything() bool {
	return c.NarrowsSources() || c.NarrowsClaims() || c.NarrowsWiki()
}

// CommandName is one name of the vault's vocabulary that a request was mapped onto, with the two
// numbers that make it checkable: how often the vault uses the name, and how many items it
// contributed to THIS set. Without the second one a mapping is an assertion; with it, it is evidence.
type CommandName struct {
	Name  string
	Kind  string // "tag" | "topic"
	Count int    // how often the vault uses the name
	Found int    // how many items of the set it produced
}

// CommandItem is one member of a command's set: a reference into the vault, plus the little a list
// row needs to stay readable after the thing it points at is gone.
type CommandItem struct {
	// Ref is a source hash or a claim id.
	Ref string
	// Title is the label the item had when the command ran.
	Title string
	// Kind is the item's own kind ("md", "pdf", "url", a claim's topic, or "wiki").
	Kind string
	// Why is the model's reason for putting THIS item in the set ("its claims are about Kamal's
	// deployment model"). A researched set is a judgement; the reason is what makes it checkable.
	Why string
}

// Command is one "request → GUI state" record: the utterance, what it was understood as, and the
// set it produced. It is a RECORD in the vault (commands/), not a page, and it is short-lived:
// the history is pruned, so the address it hands out is deliberately ephemeral.
type Command struct {
	ID       ulid.ULID
	Text     string
	Lens     CommandLens
	Criteria CommandCriteria
	// Mapped is the vocabulary the request was mapped onto, strongest first. It is what the banner
	// shows instead of a list of criteria: names with counts, not three overlapping sentences.
	Mapped []CommandName
	// Items are the members in the command's order. They are materialized because the answer must
	// not change under the reader's feet: a source deleted later simply fails to resolve.
	Items []CommandItem
	// Total is how many members the resolution found (may exceed len(Items), which is capped).
	Total int
	// Vault is how many rows the filter alone matched: the denominator a "newest N of M" note
	// needs. It is the SQL count, before the criteria that can only be applied in Go.
	Vault int
	// Scanned is how many rows the resolution actually looked at. When it is smaller than Vault the
	// answer covers only that window, and the record says so rather than implying completeness.
	Scanned int
	// URL is the address the UI navigates to. Without free text it is a plain filter address;
	// with it, the address names this record.
	URL string
	// Resolution says HOW the set came about: "filter" (deterministic, exact), "research" (a model
	// read the vault and curated it) or "ask" (a question, delegated). The banner shows it, because
	// a curated set and an exact filter are different promises.
	Resolution string
	// Rationale is the one sentence that explains the set ("9 sources whose claims are about
	// 37signals"). It is the assistant's version of Ask's prose: why THIS set and not another.
	Rationale string
	// Notes are the honest footnotes: a criterion that had to be read differently, a reference the
	// model named that does not exist, a set that was cut. They are shown, never swallowed.
	Notes []string
	// Took is how long the request took end to end. It is a record fact, not a metric: a request that
	// felt slow is easier to argue about with the number in front of you.
	Took time.Duration
	// Generated reports whether a model interpreted the utterance ("llm") or the deterministic
	// parser did ("rules"). It is a hint for the reader, never a quality claim.
	Generated string
	CreatedAt time.Time
}
