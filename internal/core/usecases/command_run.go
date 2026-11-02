package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Commands is the use case of the assistant: it reads an utterance into a lens, a filter and a
// materialized set, and it keeps that set as a record until the retention window closes.
type Commands struct {
	Index interfaces.IndexRepository
	Atoms interfaces.AtomRepository
	Wiki  interfaces.WikiStore
	// Vocab is the curated name vocabulary: its alias table turns a merged-away name into the one
	// the vault uses now.
	Vocab   interfaces.VocabularyStore
	Lexical interfaces.SearchRepository
	Store   interfaces.CommandStore
	// Gateway interprets the utterance. Nil is a supported state: the parser still works, and
	// /status says the agent is off.
	Gateway interfaces.AgentGateway
	Model   string
	Clock   interfaces.Clock
	Log     *slog.Logger
	// MaxItems bounds the materialized set. A command is a UI state, not an export: the banner
	// says when the answer was cut ("100 of 340").
	MaxItems int
	// Retention is how long a command record lives. Zero disables the pruning pass.
	Retention time.Duration
	// ScanLimit bounds how many rows one resolution looks at. The criteria that can only be applied
	// in Go (a time window, a step state) are honest about the window they covered.
	ScanLimit int
}

const (
	defaultCommandItems = 100
	defaultCommandScan  = 1000
	// commandTimeout is the classification call's ceiling. Five seconds was a lie: a thinking model
	// on a remote endpoint regularly needs more, so EVERY request fell back to the rule parser while
	// the record still said "filter" — the reader could not tell. 30 s is long enough for the cheap
	// model, short enough that a dead endpoint still answers.
	commandTimeout = 30 * time.Second
)

// CommandRunner reads an utterance into a command. A handler holds this value; nil means the
// assistant is not wired.
type CommandRunner func(ctx context.Context, text string, refs CommandRefs) (partialentities.PartialCommand, error)

func (uc Commands) Run(ctx context.Context, text string, refs CommandRefs) (partialentities.PartialCommand, error) {
	started := time.Now()
	text = strings.Join(strings.Fields(text), " ")
	if n := len([]rune(text)); n < 3 || n > 400 {
		return partialentities.PartialCommand{}, types.New("invalid_body",
			"a command needs 3 to 400 characters", nil)
	}

	parsed, generated, err := uc.interpret(ctx, text, refs)
	if err != nil {
		return partialentities.PartialCommand{}, err
	}

	// A request that points at something which is not open is refused with the reason: guessing a
	// claim would answer a different question, and answering "nothing" would be a lie about it.
	if parsed.PendingRef != "" {
		return partialentities.PartialCommand{}, types.New("unparsed",
			fmt.Sprintf("this request points at a %s, but none is open — open one or paste its id", parsed.PendingRef), nil)
	}
	// The window is a SPEC in the reading; the run turns it into the instants the index compares
	// against. "Yesterday" must mean the reader's yesterday, not the server's.
	if !parsed.Criteria.Window.Empty() {
		if err := resolveCommandWindow(&parsed.Criteria, uc.Clock.Now(), refs.Timezone); err != nil {
			return partialentities.PartialCommand{}, types.New("unparsed", err.Error(), nil)
		}
	}
	resolution := "filter"
	switch {
	case parsed.Lens == entities.CommandAsk:
		resolution = "ask"
	case parsed.Route == "research":
		resolution = "mapped"
	}
	notes := parsed.Notes
	if generated == "rules-fallback" {
		notes = append(notes,
			"read without a model: the assistant's interpretation call did not answer in time")
	}
	cmd := entities.Command{
		ID:         ulid.Make(),
		Text:       text,
		Lens:       parsed.Lens,
		Criteria:   parsed.Criteria,
		Resolution: resolution,
		Notes:      notes,
		Generated:  generated,
		CreatedAt:  uc.Clock.Now().UTC(),
	}

	if parsed.Route == "research" {
		// A subject request is answered from the vault's OWN semantic layer: the tags an LLM derived
		// for each source, the topics it gave each claim. No content is read and no candidate is
		// judged — the words are mapped onto names and the set is a join.
		if err := uc.resolveByVocabulary(ctx, &cmd); err != nil {
			return partialentities.PartialCommand{}, err
		}
	} else if err := uc.resolve(ctx, &cmd); err != nil {
		return partialentities.PartialCommand{}, err
	}
	// An explicit tag that the vault does not carry is not an answer of zero: the user named a
	// word, and the vocabulary may know the name it grew into ("cloud" → "cloud-computing"). Only
	// a tag that is genuinely absent falls back; a tag that exists but is excluded by the window or
	// the kind keeps its honest "no source matches that reading".
	if cmd.Total == 0 && cmd.Lens == entities.CommandSources && cmd.Criteria.Tag != "" &&
		cmd.Criteria.Text == "" {
		if counts, cErr := uc.Index.SourceTagCounts(ctx); cErr == nil && counts[cmd.Criteria.Tag] == 0 {
			tag := cmd.Criteria.Tag
			cmd.Notes = parsed.Notes
			cmd.Criteria.Tag = ""
			cmd.Criteria.Text = tag
			cmd.Notes = append(cmd.Notes,
				fmt.Sprintf("no source carries the tag %q; the word was mapped instead", tag))
			if err := uc.resolveVocabularyTerms(ctx, &cmd, []string{tag}); err != nil {
				return partialentities.PartialCommand{}, err
			}
			if cmd.Total > 0 {
				cmd.Resolution = "mapped"
			}
		}
	}
	// A literal word search with a window that finds nothing may be a SUBJECT the vault names
	// differently: "llm, added yesterday" is not a title containing "llm", it is the topic
	// "large-language-models" plus a window. Only the plain text+window shape falls back; a tag,
	// kind or state that excluded everything keeps its honest zero.
	if cmd.Total == 0 && cmd.Lens == entities.CommandSources && cmd.Criteria.Text != "" &&
		!cmd.Criteria.Window.Empty() && cmd.Criteria.Tag == "" && cmd.Criteria.Kind == "" &&
		cmd.Criteria.State == entities.CommandStateAny && !cmd.Criteria.BookmarksOnly &&
		!cmd.Criteria.AttachedOnly && cmd.Criteria.FromTopic == "" &&
		cmd.Criteria.Source == "" && cmd.Criteria.Atom == "" {
		cmd.Notes = parsed.Notes
		cmd.Notes = append(cmd.Notes,
			fmt.Sprintf("no source's text or claims match %q; the words were mapped instead", cmd.Criteria.Text))
		if err := uc.resolveByVocabulary(ctx, &cmd); err != nil {
			return partialentities.PartialCommand{}, err
		}
		if cmd.Total > 0 {
			cmd.Resolution = "mapped"
		}
	}
	cmd.Took = time.Since(started)
	cmd.URL = CommandURL(cmd)
	if err := uc.Store.Write(ctx, cmd); err != nil {
		return partialentities.PartialCommand{}, err
	}
	return PartialFromCommand(cmd), nil
}

// Interpret runs the model when one is configured and falls back to the parser. A model answer that
// narrows nothing is NOT an interpretation — it is the prompt's "sources, unfiltered" default, which
// would turn every misunderstanding into the whole vault. Such an answer is discarded.
func (uc Commands) interpret(ctx context.Context, text string, refs CommandRefs) (ParsedCommand, string, error) {
	// The rules decide first when they CAN decide: an enumerated filter needs no model at all, and a
	// request with a subject marker ("about", "über", "zum Thema") is a research request whatever a
	// model would say. Skipping the classification call here is what takes a subject request from
	// two round trips to one.
	if quick, err := ParseCommand(text, refs); err == nil && quick.Route != "" {
		return quick, "rules", nil
	}
	if uc.Gateway != nil {
		if parsed, ok := uc.interpretWithModel(ctx, text, refs); ok {
			return parsed, "llm", nil
		}
	}
	parsed, err := ParseCommand(text, refs)
	if err != nil {
		// The parser could not read it either. The handler turns this into an honest "I did not
		// understand that", with examples, rather than an unfiltered list.
		return ParsedCommand{}, "", err
	}
	reconcileCriteria(&parsed)
	parsed.Route = "filter"
	// The rules could not decide, a model was asked and it did not answer: that is the only case
	// worth telling the reader about. A rule-decided route used no model at all.
	generated := "rules"
	if uc.Gateway != nil {
		generated = "rules-fallback"
	}
	return parsed, generated, nil
}

// reconcileCriteria makes the reading fit the lens it was read for. A model that puts a subject in
// the claims field of a source request has understood the request — it merely used the field it
// knows for "the thing this is about". Dropping that silently is what produced a 922-row "answer".
// The reading is therefore TRANSLATED (and the translation is recorded), never discarded.
func reconcileCriteria(parsed *ParsedCommand) {
	c := &parsed.Criteria
	switch parsed.Lens {
	case entities.CommandSources, entities.CommandAttention:
		if c.Topic != "" && c.FromTopic == "" {
			c.FromTopic = c.Topic
			c.Topic = ""
			parsed.Notes = append(parsed.Notes, fmt.Sprintf(
				"\"topic\" was read as provenance: sources whose claims are about %q", c.FromTopic))
		}
	case entities.CommandClaims:
		if c.FromTopic != "" && c.Topic == "" {
			c.Topic = c.FromTopic
			c.FromTopic = ""
			parsed.Notes = append(parsed.Notes, fmt.Sprintf(
				"\"from_topic\" was read as the claim topic %q", c.Topic))
		}
		// Source-only dimensions on a claims request are not a claim filter.
		c.Tag, c.Kind, c.BookmarksOnly, c.AttachedOnly, c.State = "", "", false, false, entities.CommandStateAny
	case entities.CommandWiki:
		// A page has a title, tags, prose and an edit time. Everything else is a source or claim
		// dimension, and leaving it in the reading would be the silent-drop bug again — so it is
		// removed and NAMED. The window stays: a page is filtered by its last edit.
		if c.Kind != "" || c.State != entities.CommandStateAny || c.BookmarksOnly || c.AttachedOnly ||
			c.Source != "" || c.Atom != "" || c.FromTopic != "" || c.Topic != "" {
			parsed.Notes = append(parsed.Notes,
				"a wiki page has a title, tags, prose and an edit time; the other criteria do not apply to it")
			c.Kind, c.State, c.BookmarksOnly, c.AttachedOnly = "", entities.CommandStateAny, false, false
			c.Source, c.Atom, c.FromTopic, c.Topic = "", "", "", ""
		}
	}
}

// commandAnswer is the closed schema the model answers with. Every field is optional.
type commandAnswer struct {
	Route     string `json:"route"`
	Lens      string `json:"lens"`
	Text      string `json:"text"`
	Tag       string `json:"tag"`
	Topic     string `json:"topic"`
	Kind      string `json:"kind"`
	Window    string `json:"window"`
	Since     string `json:"since"` // legacy: a duration, read as a rolling window
	State     string `json:"state"`
	Order     string `json:"order"`
	Bookmarks bool   `json:"bookmarks"`
	Attached  bool   `json:"attached"`
	Source    string `json:"source"`
	Atom      string `json:"atom"`
}

func (uc Commands) interpretWithModel(ctx context.Context, text string, refs CommandRefs) (ParsedCommand, bool) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	system, user := agents.CommandMessages(text, uc.topTags(ctx), uc.topTopics(ctx), refs.SourceHash(), refs.AtomID(), refs.Timezone, uc.Clock.Now())
	resp, err := uc.Gateway.Complete(ctx, interfaces.AgentRequest{
		Model: uc.Model,
		Messages: []interfaces.AgentMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		uc.warn(ctx, "the assistant could not reach the model", err)
		return ParsedCommand{}, false
	}
	answer, err := decodeCommandAnswer(resp.Message.Content)
	if err != nil {
		uc.warn(ctx, "the assistant could not read the model's answer", err)
		return ParsedCommand{}, false
	}

	if answer.Route == "ask" {
		// The route decides the destination: a question is answered by Ask, whatever lens the model
		// also named.
		answer.Lens = string(entities.CommandAsk)
	}
	parsed := ParsedCommand{Route: answer.Route, Lens: entities.CommandLens(answer.Lens), Criteria: entities.CommandCriteria{
		Text:          strings.TrimSpace(answer.Text),
		Tag:           strings.ToLower(strings.TrimSpace(answer.Tag)),
		Topic:         strings.ToLower(strings.TrimSpace(answer.Topic)),
		Kind:          strings.TrimSpace(answer.Kind),
		State:         entities.CommandState(answer.State),
		Order:         entities.CommandOrder(answer.Order),
		BookmarksOnly: answer.Bookmarks,
		AttachedOnly:  answer.Attached,
		Source:        strings.TrimSpace(answer.Source),
		Atom:          strings.TrimSpace(answer.Atom),
	}}
	if !parsed.Lens.Valid() {
		parsed.Lens = entities.CommandSources
	}
	if answer.Window != "" {
		parsed.Criteria.Window.Spec = normalizeWindowSpec(answer.Window)
	} else if answer.Since != "" {
		// A model that still answers the old schema is read as the rolling window it meant.
		if d, ok := parseWindowDuration(answer.Since); ok && d > 0 {
			parsed.Criteria.Window.Spec = fmt.Sprintf("last-%dh", int(d.Hours()))
		}
	}
	switch parsed.Criteria.Kind {
	case "md", "pdf", "txt", "epub", "url":
	default:
		parsed.Criteria.Kind = ""
	}
	switch parsed.Criteria.State {
	case entities.CommandStateOpen, entities.CommandStateFailed, entities.CommandStateDone:
	default:
		parsed.Criteria.State = entities.CommandStateAny
	}
	switch parsed.Criteria.Order {
	case entities.CommandOrderOldest, entities.CommandOrderTitle:
	default:
		parsed.Criteria.Order = entities.CommandOrderNewest
	}
	// The lens decides which fields mean something. Translating (not dropping) keeps a good reading
	// alive; the invariant in resolve() then refuses one that narrows nothing.
	reconcileCriteria(&parsed)
	switch parsed.Route {
	case "research", "ask":
		// The route comes from the model: a research request deliberately names a subject that no
		// filter field expresses, so "narrowed nothing" is not a reason to refuse it.
		if parsed.Lens == "" {
			parsed.Lens = entities.CommandSources
		}
		return parsed, true
	}
	parsed.Route = "filter"
	if parsed.Criteria.IsEmpty() && parsed.Lens != entities.CommandAsk {
		return ParsedCommand{}, false
	}
	return parsed, true
}

// decodeCommandAnswer accepts the JSON object even when the model wrapped it in a code fence.
func decodeCommandAnswer(content string) (commandAnswer, error) {
	body := strings.TrimSpace(content)
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	var answer commandAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		return commandAnswer{}, err
	}
	return answer, nil
}

// normalizeWindowSpec accepts the model's small variations of the closed vocabulary: "7d" is the
// same window as "last-7d", and "this week" is "this-week".
func normalizeWindowSpec(raw string) string {
	spec := strings.ToLower(strings.TrimSpace(raw))
	if spec == "" {
		return ""
	}
	switch spec {
	case "this week":
		return "this-week"
	case "last week":
		return "last-week"
	case "this month":
		return "this-month"
	case "last month":
		return "last-month"
	case "since yesterday":
		return "since-yesterday"
	}
	if strings.HasPrefix(spec, "last-") || strings.Contains(spec, "..") {
		return spec
	}
	if len(spec) >= 2 {
		unit := spec[len(spec)-1]
		if unit == 'h' || unit == 'd' || unit == 'w' || unit == 'm' {
			if _, err := strconv.Atoi(spec[:len(spec)-1]); err == nil {
				return "last-" + spec
			}
		}
	}
	return spec
}

// topTags and topTopics are the model's vocabulary: a tag it may pick must be one the vault has, so
// a plausible word cannot become a filter that matches nothing.
func (uc Commands) topTags(ctx context.Context) []string {
	counts, err := uc.Index.SourceTagCounts(ctx)
	if err != nil {
		return nil
	}
	return topNames(counts, 40)
}

func (uc Commands) topTopics(ctx context.Context) []string {
	uses, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return nil
	}
	// The 60 topics the vault uses MOST, not the alphabetically first 60: a vault has tens of
	// thousands of topics, and a sorted head is a vocabulary the model can never match.
	sort.Slice(uses, func(i, j int) bool {
		if uses[i].Count != uses[j].Count {
			return uses[i].Count > uses[j].Count
		}
		return uses[i].Topic < uses[j].Topic
	})
	if len(uses) > 60 {
		uses = uses[:60]
	}
	names := make([]string, 0, len(uses))
	for _, u := range uses {
		names = append(names, u.Topic)
	}
	return names
}

func topNames(counts map[string]int, limit int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > limit {
		names = names[:limit]
	}
	return names
}

func (uc Commands) warn(ctx context.Context, msg string, err error) {
	if uc.Log != nil {
		uc.Log.WarnContext(ctx, msg, "error", err)
	}
}

// resolve materializes the set. It is deterministic: the rows, the window and the order all come
// from the index, and the criteria that the index cannot express are applied here, in Go.
func (uc Commands) resolve(ctx context.Context, cmd *entities.Command) error {
	switch cmd.Lens {
	case entities.CommandSources, entities.CommandAttention:
		// THE invariant. A source list is answered only when the reading says something about a
		// SOURCE. A criterion that belongs to another lens (a claim topic, say) is not a filter:
		// answering with the whole vault looks like a result and is the worst thing this feature
		// can do. It is caught here, after the reading, before anything is written.
		if !cmd.Criteria.NarrowsSources() {
			return types.New("unparsed",
				"nothing in that request narrows a source list (a topic is a claim dimension)", nil)
		}
		return uc.resolveSources(ctx, cmd)
	case entities.CommandClaims:
		if !cmd.Criteria.NarrowsClaims() {
			return types.New("unparsed", "nothing in that request narrows a claim list", nil)
		}
		return uc.resolveClaims(ctx, cmd)
	case entities.CommandWiki:
		// The wiki IS a list: the reader's own pages, with a title, tags and prose. A request that
		// names one of those must show the matching pages — "Wiki pages about X" answering with an
		// unfiltered list is the same lie as a source list answering with the whole vault.
		if !cmd.Criteria.NarrowsWiki() {
			return types.New("unparsed", "nothing in that request narrows the wiki list", nil)
		}
		return uc.resolveWiki(ctx, cmd)
	case entities.CommandStatus:
		// The report is a destination, not a list: a criterion attached to it was misread.
		if cmd.Criteria.NarrowsAnything() {
			return types.New("unparsed", "the vault report is a destination, not a filtered list", nil)
		}
		return nil
	default:
		// an ask delegates to the query agent in the UI.
		return nil
	}
}

func (uc Commands) resolveSources(ctx context.Context, cmd *entities.Command) error {
	crit := cmd.Criteria
	if crit.Atom != "" || crit.Source != "" {
		return uc.resolveSourcesByRefs(ctx, cmd)
	}
	if crit.FromTopic != "" {
		return uc.resolveSourcesByTopic(ctx, cmd)
	}
	filter := entities.SourceFilter{
		Kind: crit.Kind,
		// The words are a LITERAL match over the source's own text fields, in SQL, so a window over
		// an old part of the vault is not lost to the list's scan limit. When nothing matches there,
		// the claims that mention the words are the second channel (a person's name usually lives
		// in the claims, not in a URL).
		Query:         crit.Text,
		Tag:           crit.Tag,
		AttachedOnly:  crit.AttachedOnly,
		BookmarksOnly: crit.BookmarksOnly,
		FailedOnly:    cmd.Lens == entities.CommandAttention,
		Since:         crit.Window.From,
		Until:         crit.Window.To,
	}
	scan := uc.scanLimit()

	vault, err := uc.Index.CountSources(ctx, filter)
	if err != nil {
		return err
	}
	srcs, err := uc.Index.ListSources(ctx, filter, scan, 0)
	if err != nil {
		return err
	}
	if crit.Text != "" && len(srcs) == 0 {
		mentioned, note, mErr := uc.sourcesMentioningText(ctx, crit.Text, crit, cmd.Lens, scan)
		if mErr != nil {
			return mErr
		}
		if len(mentioned) > 0 {
			srcs, vault = mentioned, len(mentioned)
			if note != "" {
				cmd.Notes = append(cmd.Notes, note)
			}
		}
	}

	kept := make([]entities.Source, 0, len(srcs))
	for _, s := range srcs {
		if !sourceStateMatches(s, crit.State) {
			continue
		}
		kept = append(kept, s)
	}
	orderSources(kept, crit.Order)

	cmd.Total = len(kept)
	cmd.Vault = vault
	cmd.Scanned = len(srcs)
	items := make([]entities.CommandItem, 0, min(len(kept), uc.maxItems()))
	for _, s := range kept[:min(len(kept), uc.maxItems())] {
		items = append(items, entities.CommandItem{Ref: s.Hash, Title: s.Title, Kind: string(s.Kind)})
	}
	cmd.Items = items
	if cmd.Total == 0 {
		if crit.Text != "" {
			cmd.Notes = append(cmd.Notes,
				fmt.Sprintf("no source's title, URL or tag and no claim mentions %q", crit.Text))
		} else {
			cmd.Notes = append(cmd.Notes, "no source matches that reading")
		}
	}
	return nil
}

// sourcesMentioningText is the second lexical channel: the claims that mention the words, mapped to
// the sources that own them. It is strict (every word, no OR fallback), so an absent name returns
// nothing instead of a page that merely shares a word.
func (uc Commands) sourcesMentioningText(ctx context.Context, text string, crit entities.CommandCriteria, lens entities.CommandLens, limit int) ([]entities.Source, string, error) {
	if uc.Lexical == nil {
		return nil, "", nil
	}
	hits, err := uc.Lexical.SearchExact(ctx, text, "atoms", limit)
	if err != nil {
		return nil, "", err
	}
	ids := make([]ulid.ULID, 0, len(hits))
	for _, hit := range hits {
		if id, parseErr := ulid.ParseStrict(hit.ID); parseErr == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, "", nil
	}
	// The window belongs to the SOURCE lens (its arrival), not to the claim's creation: a source
	// imported yesterday may own an old claim that mentions the name.
	provenance, err := uc.Atoms.SourcesForAtoms(ctx, ids)
	if err != nil {
		return nil, "", err
	}
	counts := map[string]int{}
	for _, sources := range provenance {
		for _, src := range sources {
			if src.Hash != "" {
				counts[src.Hash]++
			}
		}
	}
	type scored struct {
		src      entities.Source
		mentions int
	}
	kept := make([]scored, 0, len(counts))
	for hash, mentions := range counts {
		src, gErr := uc.Index.GetSourceByHash(ctx, hash)
		if gErr != nil {
			continue
		}
		if lens == entities.CommandAttention && !sourceFailed(src) {
			continue
		}
		if !sourceMatchesCriteria(src, crit) {
			continue
		}
		kept = append(kept, scored{src: src, mentions: mentions})
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].mentions != kept[j].mentions {
			return kept[i].mentions > kept[j].mentions
		}
		return kept[i].src.ImportedAt.After(kept[j].src.ImportedAt)
	})
	out := make([]entities.Source, 0, len(kept))
	for _, item := range kept {
		out = append(out, item.src)
	}
	noun := "claims"
	if len(ids) == 1 {
		noun = "claim"
	}
	return out, fmt.Sprintf("%d %s mention %q", len(ids), noun, text), nil
}

// resolveSourcesFromCounts turns "a set of sources, scored" into the command's materialized set.
// The three ways into it — a topic's claims, one claim's provenance, one named source — all end
// here, so the filtering, ordering and honest notes cannot drift apart.
func (uc Commands) resolveSourcesFromCounts(ctx context.Context, cmd *entities.Command, counts map[string]int, mapped bool) error {
	crit := cmd.Criteria

	type scored struct {
		src    entities.Source
		claims int
	}
	kept := make([]scored, 0, len(counts))
	for hash, claims := range counts {
		src, gErr := uc.Index.GetSourceByHash(ctx, hash)
		if gErr != nil {
			continue
		}
		if cmd.Lens == entities.CommandAttention && !sourceFailed(src) {
			continue
		}
		if !sourceMatchesCriteria(src, crit) {
			continue
		}
		kept = append(kept, scored{src: src, claims: claims})
	}
	sort.Slice(kept, func(i, j int) bool {
		if crit.Order == entities.CommandOrderTitle {
			return kept[i].src.Title < kept[j].src.Title
		}
		if kept[i].claims != kept[j].claims {
			return kept[i].claims > kept[j].claims
		}
		if crit.Order == entities.CommandOrderOldest {
			return kept[i].src.ImportedAt.Before(kept[j].src.ImportedAt)
		}
		return kept[i].src.ImportedAt.After(kept[j].src.ImportedAt)
	})

	cmd.Total = len(kept)
	// A mapped set is a join over names and claims, not a scan of the vault: pretending its
	// candidate union is the vault denominator makes the banner lie about completeness.
	if mapped {
		cmd.Vault, cmd.Scanned = 0, 0
	} else {
		cmd.Vault, cmd.Scanned = len(counts), len(counts)
	}
	items := make([]entities.CommandItem, 0, min(len(kept), uc.maxItems()))
	for _, s := range kept[:min(len(kept), uc.maxItems())] {
		items = append(items, entities.CommandItem{Ref: s.src.Hash, Title: s.src.Title, Kind: string(s.src.Kind)})
	}
	cmd.Items = items
	return nil
}

// resolveSourcesByTopic answers "sources ABOUT X": the sources that own claims with that topic.
//
// This is the difference between a filter and a question. "37signals" is not a tag and not a
// substring of a title — it is what ten claims are about, and the sources behind them are the
// answer. The join is exact and cheap (the topic's claims carry their provenance), and the other
// criteria still narrow within it.
func (uc Commands) resolveSourcesByTopic(ctx context.Context, cmd *entities.Command) error {
	crit := cmd.Criteria
	atoms, err := uc.Atoms.ListAtoms(ctx, crit.FromTopic, uc.scanLimit(), 0)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, a := range atoms {
		for _, src := range a.Sources {
			if src.Hash != "" {
				counts[src.Hash]++
			}
		}
	}
	if err := uc.resolveSourcesFromCounts(ctx, cmd, counts, false); err != nil {
		return err
	}
	if len(atoms) > 0 {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("%d claims carry the topic %q", len(atoms), crit.FromTopic))
	}
	if len(atoms) == uc.scanLimit() {
		cmd.Notes = append(cmd.Notes,
			fmt.Sprintf("read the newest %d claims of topic %q; the topic may have more", len(atoms), crit.FromTopic))
	}
	if cmd.Total == 0 {
		cmd.Notes = append(cmd.Notes,
			fmt.Sprintf("no source owns a claim about %q", crit.FromTopic))
	}
	return nil
}

// resolveSourcesByRefs answers a request that POINTS at something: one claim (its provenance is
// the answer) or one source (a set of exactly one, or the base the other criteria narrow).
func (uc Commands) resolveSourcesByRefs(ctx context.Context, cmd *entities.Command) error {
	crit := cmd.Criteria
	counts := map[string]int{}
	switch {
	case crit.Atom != "":
		id, err := ulid.ParseStrict(crit.Atom)
		if err != nil {
			return types.New("invalid_id", "the referenced claim id is not a ULID", types.ErrInvalidID)
		}
		atom, err := uc.Atoms.GetAtom(ctx, id)
		if err != nil {
			return err
		}
		for _, src := range atom.Sources {
			if src.Hash != "" {
				counts[src.Hash]++
			}
		}
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("provenance of the claim %q", truncateClaim(atom.Claim)))
	case crit.Source != "":
		if !entities.IsSHA256Hex(crit.Source) {
			return types.New("invalid_hash", "the referenced source is not a SHA-256 hash", types.ErrInvalidHash)
		}
		counts[crit.Source] = 1
	}

	if err := uc.resolveSourcesFromCounts(ctx, cmd, counts, false); err != nil {
		return err
	}
	if cmd.Total == 0 {
		cmd.Notes = append(cmd.Notes, "the referenced item has no source in this vault")
	}
	return nil
}

func truncateClaim(claim string) string {
	if len(claim) <= 70 {
		return claim
	}
	return claim[:70] + "…"
}

// sourceFailed is the Attention lens's own rule: it shows what needs a human, so a source without a
// failed step is not part of its set.
func sourceFailed(s entities.Source) bool {
	for _, step := range []entities.Step{entities.StepCapture, entities.StepOutline, entities.StepDistill} {
		if s.Steps.State(step) == entities.StepFailed {
			return true
		}
	}
	return false
}

// sourceMatchesCriteria applies the criteria the index cannot express (or that the join has to
// apply on top of the topic set) to one source.
func sourceMatchesCriteria(src entities.Source, crit entities.CommandCriteria) bool {
	if crit.Kind != "" && string(src.Kind) != crit.Kind {
		return false
	}
	if crit.Tag != "" && !slices.Contains(src.Tags, crit.Tag) && !slices.Contains(src.AutoTags, crit.Tag) {
		return false
	}
	if crit.AttachedOnly && !src.Attached {
		return false
	}
	if crit.BookmarksOnly && !src.Bookmark {
		return false
	}
	if !WindowContains(src.ImportedAt, crit.Window.From, crit.Window.To) {
		return false
	}
	return sourceStateMatches(src, crit.State)
}

// resolveWiki answers "my own pages about X". Wiki pages are few and local, so the match is done
// here over title, tags and prose — the same "one word is enough, more words rank higher" ladder
// the lexical search uses, without an index that does not exist for them.
func (uc Commands) resolveWiki(ctx context.Context, cmd *entities.Command) error {
	if uc.Wiki == nil {
		return types.New("not_implemented", "the wiki is not configured", types.ErrNotImplemented)
	}
	pages, err := uc.Wiki.List(ctx)
	if err != nil {
		return err
	}
	crit := cmd.Criteria
	words := strings.Fields(strings.ToLower(crit.Text))
	wanted := map[string]bool{}
	for _, tag := range crit.Tags {
		wanted[strings.ToLower(tag)] = true
	}
	if crit.Tag != "" {
		wanted[strings.ToLower(crit.Tag)] = true
	}
	explicitTag := crit.Tag != ""

	type scored struct {
		page  entities.WikiPage
		score int
	}
	kept := make([]scored, 0, len(pages))
	var partial []scored
	found := map[string]int{}
	textFound := 0
	for _, page := range pages {
		title := strings.ToLower(page.Title)
		body := strings.ToLower(page.Body)
		tags := make([]string, 0, len(page.Tags)+len(page.AutoTags))
		for _, tag := range append(append([]string{}, page.Tags...), page.AutoTags...) {
			tags = append(tags, strings.ToLower(tag))
		}
		tagMatch := false
		for _, tag := range tags {
			if wanted[tag] {
				tagMatch = true
				found["tag:"+tag]++
			}
		}
		// An explicitly named tag is a hard filter; the mapped tags are a signal beside the text.
		if explicitTag && !tagMatch {
			continue
		}
		if len(wanted) > 0 && !tagMatch && crit.Text == "" {
			continue
		}
		// A page's own clock is its last edit; an untouched page falls back to its creation.
		pageTime := page.UpdatedAt
		if pageTime.IsZero() {
			pageTime = page.CreatedAt
		}
		if !WindowContains(pageTime, crit.Window.From, crit.Window.To) {
			continue
		}
		// Every word must appear SOMEWHERE (title, tag or prose). A generic word in the body was
		// enough to drag an unrelated page in — "GitHub projects" matched four pages that merely
		// say "projects" once.
		score, matchedAll := 0, true
		for _, word := range words {
			switch {
			case strings.Contains(title, word):
				score += 3
			case slices.Contains(tags, word) || strings.Contains(strings.Join(tags, " "), word):
				score += 2
			case strings.Contains(body, word):
				score++
			default:
				matchedAll = false
			}
		}
		if len(words) > 0 && score > 0 {
			textFound++
		}
		if len(words) > 0 && !matchedAll {
			if score == 0 {
				continue
			}
			partial = append(partial, scored{page: page, score: score})
			continue
		}
		kept = append(kept, scored{page: page, score: score})
	}
	// Only when NOTHING has all the words is the reading relaxed, and then it says so.
	relaxed := false
	if len(kept) == 0 && len(partial) > 0 {
		kept, relaxed = partial, true
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].score != kept[j].score {
			return kept[i].score > kept[j].score
		}
		return kept[i].page.UpdatedAt.After(kept[j].page.UpdatedAt)
	})

	cmd.Total = len(kept)
	cmd.Vault = len(pages)
	cmd.Scanned = len(pages)
	items := make([]entities.CommandItem, 0, min(len(kept), uc.maxItems()))
	for _, s := range kept[:min(len(kept), uc.maxItems())] {
		items = append(items, entities.CommandItem{Ref: s.page.ID.String(), Title: s.page.Title, Kind: "wiki"})
	}
	cmd.Items = items
	if len(crit.Tags) > 0 {
		for _, tag := range crit.Tags {
			if n := found["tag:"+strings.ToLower(tag)]; n > 0 {
				cmd.Mapped = append(cmd.Mapped, entities.CommandName{
					Name: tag, Kind: "tag", Count: n, Found: n,
				})
			}
		}
		if crit.Text != "" && textFound > 0 {
			cmd.Mapped = append(cmd.Mapped, entities.CommandName{
				Name: crit.Text, Kind: "text", Count: textFound, Found: textFound,
			})
		}
		sortMapped(cmd.Mapped)
	}
	if relaxed {
		cmd.Notes = append(cmd.Notes,
			fmt.Sprintf("no page has all of %s; these match at least one", strings.Join(words, ", ")))
	}
	if cmd.Total == 0 {
		cmd.Notes = append(cmd.Notes, "no wiki page matches that reading")
	}
	return nil
}

func (uc Commands) resolveClaims(ctx context.Context, cmd *entities.Command) error {
	crit := cmd.Criteria
	scan := uc.scanLimit()

	// A reference is exact: one claim, or one source's claims. No search, no ranking.
	if crit.Atom != "" {
		id, err := ulid.ParseStrict(crit.Atom)
		if err != nil {
			return types.New("invalid_id", "the referenced claim id is not a ULID", types.ErrInvalidID)
		}
		atom, err := uc.Atoms.GetAtom(ctx, id)
		if err != nil {
			return err
		}
		if !WindowContains(atom.CreatedAt, crit.Window.From, crit.Window.To) {
			cmd.Total, cmd.Vault, cmd.Scanned = 0, 1, 1
			cmd.Notes = append(cmd.Notes, "the referenced claim is outside the time window")
			return nil
		}
		cmd.Items = []entities.CommandItem{{Ref: atom.ID.String(), Title: atom.Claim, Kind: atom.Topic}}
		cmd.Total, cmd.Vault, cmd.Scanned = 1, 1, 1
		return nil
	}
	if crit.Source != "" {
		if !entities.IsSHA256Hex(crit.Source) {
			return types.New("invalid_hash", "the referenced source is not a SHA-256 hash", types.ErrInvalidHash)
		}
		atoms, err := uc.Atoms.ListAtomsBySource(ctx, crit.Source)
		if err != nil {
			return err
		}
		atoms = filterAtomsByWindow(atoms, crit.Window.From, crit.Window.To)
		if crit.Order == entities.CommandOrderOldest {
			slices.Reverse(atoms)
		}
		items := make([]entities.CommandItem, 0, min(len(atoms), uc.maxItems()))
		for _, a := range atoms {
			if len(items) >= uc.maxItems() {
				break
			}
			items = append(items, entities.CommandItem{Ref: a.ID.String(), Title: a.Claim, Kind: a.Topic})
		}
		cmd.Items = items
		cmd.Total, cmd.Vault, cmd.Scanned = len(atoms), len(atoms), len(atoms)
		return nil
	}

	items := make([]entities.CommandItem, 0, uc.maxItems())
	if crit.Text != "" {
		if uc.Lexical == nil {
			cmd.Total, cmd.Vault, cmd.Scanned = 0, 0, 0
			cmd.Notes = append(cmd.Notes, "the lexical search is not configured")
			return nil
		}
		hits, err := uc.Lexical.Search(ctx, crit.Text, "atoms", scan)
		if err != nil {
			return err
		}
		cmd.Scanned = len(hits)
		ids := make([]ulid.ULID, 0, len(hits))
		for _, hit := range hits {
			if id, parseErr := ulid.ParseStrict(hit.ID); parseErr == nil {
				ids = append(ids, id)
			}
		}
		byID := map[ulid.ULID]entities.Atom{}
		if atoms, aErr := uc.Atoms.AtomTextsForIDs(ctx, ids); aErr == nil {
			for _, atom := range atoms {
				byID[atom.ID] = atom
			}
		}
		matched := 0
		for _, hit := range hits {
			id, parseErr := ulid.ParseStrict(hit.ID)
			if parseErr != nil {
				continue
			}
			atom, ok := byID[id]
			if !ok {
				continue
			}
			if crit.Topic != "" && !strings.EqualFold(atom.Topic, crit.Topic) {
				continue
			}
			if !WindowContains(atom.CreatedAt, crit.Window.From, crit.Window.To) {
				continue
			}
			matched++
			if len(items) >= uc.maxItems() {
				continue
			}
			items = append(items, entities.CommandItem{Ref: hit.ID, Title: hit.Title, Kind: atom.Topic})
		}
		cmd.Total = matched
	} else {
		atoms, err := uc.Atoms.ListAtoms(ctx, crit.Topic, scan, 0)
		if err != nil {
			return err
		}
		cmd.Scanned = len(atoms)
		atoms = filterAtomsByWindow(atoms, crit.Window.From, crit.Window.To)
		if crit.Order == entities.CommandOrderOldest {
			slices.Reverse(atoms)
		}
		if crit.Order == entities.CommandOrderTitle {
			sort.Slice(atoms, func(i, j int) bool { return atoms[i].Claim < atoms[j].Claim })
		}
		cmd.Total = len(atoms)
		for _, a := range atoms {
			if len(items) >= uc.maxItems() {
				break
			}
			items = append(items, entities.CommandItem{Ref: a.ID.String(), Title: a.Claim, Kind: a.Topic})
		}
		if len(atoms) == scan {
			cmd.Notes = append(cmd.Notes,
				fmt.Sprintf("read the newest %d claims; a wider window may have more", scan))
		}
	}
	cmd.Items = items
	cmd.Vault = cmd.Scanned
	if cmd.Total == 0 {
		cmd.Notes = append(cmd.Notes, "no claim matches that reading")
	}
	return nil
}

// filterAtomsByWindow keeps the claims created inside the window. A claim's clock is its creation,
// not its source's arrival; the two lenses answer different questions.
func filterAtomsByWindow(atoms []entities.Atom, from, to time.Time) []entities.Atom {
	if from.IsZero() && to.IsZero() {
		return atoms
	}
	out := atoms[:0]
	for _, atom := range atoms {
		if WindowContains(atom.CreatedAt, from, to) {
			out = append(out, atom)
		}
	}
	return out
}

// sourceStateMatches reads the traffic lights: "open" means the derivation is not finished and has
// not failed, "done" means every document step is ok or does not apply.
func sourceStateMatches(s entities.Source, want entities.CommandState) bool {
	if want == entities.CommandStateAny {
		return true
	}
	failed, done := false, true
	for _, step := range []entities.Step{entities.StepCapture, entities.StepOutline, entities.StepDistill} {
		switch s.Steps.State(step) {
		case entities.StepFailed:
			failed = true
			done = false
		case entities.StepOK, entities.StepSkipped:
		default:
			done = false
		}
	}
	switch want {
	case entities.CommandStateFailed:
		return failed
	case entities.CommandStateDone:
		return done
	case entities.CommandStateOpen:
		return !failed && !done
	}
	return true
}

func orderSources(srcs []entities.Source, order entities.CommandOrder) {
	switch order {
	case entities.CommandOrderOldest:
		slices.Reverse(srcs)
	case entities.CommandOrderTitle:
		sort.Slice(srcs, func(i, j int) bool { return srcs[i].Title < srcs[j].Title })
	}
}

func (uc Commands) maxItems() int {
	if uc.MaxItems > 0 {
		return uc.MaxItems
	}
	return defaultCommandItems
}

func (uc Commands) scanLimit() int {
	if uc.ScanLimit > 0 {
		return uc.ScanLimit
	}
	return defaultCommandScan
}

// CommandURL is the address a command hands to the UI. It is built HERE, from the record — never by
// the model, and never from free text: an address that carries the words of a question is a
// different address on every visit.
func CommandURL(c entities.Command) string {
	id := c.ID.String()
	switch c.Lens {
	case entities.CommandAsk:
		return "#/ask"
	case entities.CommandStatus:
		return "#/status"
	case entities.CommandWiki:
		return "#/wiki?command=" + id
	}

	params := url.Values{}
	switch c.Lens {
	case entities.CommandClaims:
		if c.Criteria.Topic != "" {
			params.Set("topic", c.Criteria.Topic)
		}
	default:
		if c.Criteria.Tag != "" {
			params.Set("tag", c.Criteria.Tag)
		}
		if c.Criteria.Kind != "" {
			params.Set("kind", c.Criteria.Kind)
		}
		if c.Criteria.BookmarksOnly {
			params.Set("bookmarks", "1")
		}
		if c.Criteria.AttachedOnly {
			params.Set("attached", "1")
		}
	}
	// The id is always part of the address: it is what makes the result recognisable as the
	// assistant's, and what the lens reads to find the materialized set. The named criteria travel
	// beside it, so the list still means something after the record has been pruned.
	params.Set("command", id)

	path := "sources"
	switch c.Lens {
	case entities.CommandClaims:
		path = "claims"
	case entities.CommandAttention:
		path = "attention"
	}
	return "#/" + path + "?" + params.Encode()
}

// PartialFromMapped projects the mapped vocabulary names.
func PartialFromMapped(mapped []entities.CommandName) []partialentities.PartialCommandName {
	out := make([]partialentities.PartialCommandName, 0, len(mapped))
	for _, m := range mapped {
		out = append(out, partialentities.PartialCommandName{Name: m.Name, Kind: m.Kind, Count: m.Count, Found: m.Found})
	}
	return out
}

// PartialFromCommand is the wire projection of a command.
func PartialFromCommand(c entities.Command) partialentities.PartialCommand {
	items := make([]partialentities.PartialCommandItem, 0, len(c.Items))
	for _, it := range c.Items {
		items = append(items, partialentities.PartialCommandItem{Ref: it.Ref, Title: it.Title, Kind: it.Kind, Why: it.Why})
	}
	return partialentities.PartialCommand{
		ID: c.ID, Text: c.Text, Lens: string(c.Lens), URL: c.URL,
		Resolution: c.Resolution, Rationale: c.Rationale, Notes: c.Notes,
		Took:   c.Took,
		Mapped: PartialFromMapped(c.Mapped),
		Total:  c.Total, Vault: c.Vault, Scanned: c.Scanned, Generated: c.Generated,
		CreatedAt: c.CreatedAt, Items: items,
		Criteria: partialentities.PartialCommandCriteria{
			Text: c.Criteria.Text, Terms: c.Criteria.Terms,
			Tag: c.Criteria.Tag, Kind: c.Criteria.Kind,
			Topic: c.Criteria.Topic, FromTopic: c.Criteria.FromTopic,
			Tags: c.Criteria.Tags, Topics: c.Criteria.Topics,
			Source: c.Criteria.Source, Atom: c.Criteria.Atom,
			Window: partialentities.PartialCommandWindow{
				Spec: c.Criteria.Window.Spec, From: c.Criteria.Window.From,
				To: c.Criteria.Window.To, Timezone: c.Criteria.Window.Timezone,
			},
			State: string(c.Criteria.State),
			Order: string(c.Criteria.Order), BookmarksOnly: c.Criteria.BookmarksOnly,
			AttachedOnly: c.Criteria.AttachedOnly,
		},
	}
}
