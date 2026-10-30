package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/agents"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// RunCommand executes the assistant: one utterance in, one persisted command out.
//
// The utterance is READ, never answered. A model may interpret it (a closed schema over this app's
// own dimensions), and the deterministic parser is both the no-model path and the fallback when the
// model's answer is unusable. The resolution itself is deterministic Go over the index: which rows
// match, in which order, is never left to the model.
type RunCommand struct {
	Index interfaces.IndexRepository
	Atoms interfaces.AtomRepository
	Wiki  interfaces.WikiStore
	// Vocab is the curated name vocabulary: its alias table turns a merged-away name into the one
	// the vault uses now.
	Vocab    interfaces.VocabularyStore
	Search   interfaces.SearchRepository
	Commands interfaces.CommandStore
	// Gateway interprets the utterance. Nil is a supported state: the parser still works, and
	// /status says the agent is off.
	Gateway interfaces.AgentGateway
	Model   string
	Clock   interfaces.Clock
	Log     *slog.Logger
	// MaxItems bounds the materialized set. A command is a UI state, not an export: the banner
	// says when the answer was cut ("100 of 340").
	MaxItems int
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

// Execute reads the utterance, resolves it and writes the record.
func (uc RunCommand) Execute(ctx context.Context, text string, refs CommandRefs) (partialentities.PartialCommand, error) {
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
	cmd.Took = time.Since(started)
	cmd.URL = CommandURL(cmd)
	if err := uc.Commands.Write(ctx, cmd); err != nil {
		return partialentities.PartialCommand{}, err
	}
	return PartialFromCommand(cmd), nil
}

// Interpret runs the model when one is configured and falls back to the parser. A model answer that
// narrows nothing is NOT an interpretation — it is the prompt's "sources, unfiltered" default, which
// would turn every misunderstanding into the whole vault. Such an answer is discarded.
func (uc RunCommand) interpret(ctx context.Context, text string, refs CommandRefs) (ParsedCommand, string, error) {
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
		// A page has a title, tags and prose. Everything else is a source or claim dimension, and
		// leaving it in the reading would be the silent-drop bug again — so it is removed and NAMED.
		if c.Kind != "" || c.State != entities.CommandStateAny || c.BookmarksOnly || c.AttachedOnly ||
			c.Source != "" || c.Atom != "" || c.FromTopic != "" || c.Topic != "" || c.Since > 0 {
			parsed.Notes = append(parsed.Notes,
				"a wiki page has a title, tags and prose; the other criteria do not apply to it")
			c.Kind, c.State, c.BookmarksOnly, c.AttachedOnly = "", entities.CommandStateAny, false, false
			c.Source, c.Atom, c.FromTopic, c.Topic, c.Since = "", "", "", "", 0
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
	Since     string `json:"since"`
	State     string `json:"state"`
	Order     string `json:"order"`
	Bookmarks bool   `json:"bookmarks"`
	Attached  bool   `json:"attached"`
	Source    string `json:"source"`
	Atom      string `json:"atom"`
}

func (uc RunCommand) interpretWithModel(ctx context.Context, text string, refs CommandRefs) (ParsedCommand, bool) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	system, user := agents.CommandMessages(text, uc.topTags(ctx), uc.topTopics(ctx), refs.SourceHash(), refs.AtomID())
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
	if answer.Since != "" {
		if d, ok := parseWindowDuration(answer.Since); ok && d > 0 {
			parsed.Criteria.Since = d
		}
	}
	switch parsed.Criteria.Kind {
	case "md", "pdf", "txt", "url":
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

// topTags and topTopics are the model's vocabulary: a tag it may pick must be one the vault has, so
// a plausible word cannot become a filter that matches nothing.
func (uc RunCommand) topTags(ctx context.Context) []string {
	counts, err := uc.Index.SourceTagCounts(ctx)
	if err != nil {
		return nil
	}
	return topNames(counts, 40)
}

func (uc RunCommand) topTopics(ctx context.Context) []string {
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

func (uc RunCommand) warn(ctx context.Context, msg string, err error) {
	if uc.Log != nil {
		uc.Log.WarnContext(ctx, msg, "error", err)
	}
}

// resolve materializes the set. It is deterministic: the rows, the window and the order all come
// from the index, and the criteria that the index cannot express are applied here, in Go.
func (uc RunCommand) resolve(ctx context.Context, cmd *entities.Command) error {
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

func (uc RunCommand) resolveSources(ctx context.Context, cmd *entities.Command) error {
	crit := cmd.Criteria
	if crit.Atom != "" || crit.Source != "" {
		return uc.resolveSourcesByRefs(ctx, cmd)
	}
	if crit.FromTopic != "" {
		return uc.resolveSourcesByTopic(ctx, cmd)
	}
	filter := entities.SourceFilter{
		Kind: crit.Kind,
		// The text is matched below, word by word: a substring filter ("Software Architektur") finds
		// nothing in a vault that says "software" and "architecture" separately, and a request that
		// returns 0 for a phrase the vault is full of looks like a broken feature.
		Tag:           crit.Tag,
		AttachedOnly:  crit.AttachedOnly,
		BookmarksOnly: crit.BookmarksOnly,
		FailedOnly:    cmd.Lens == entities.CommandAttention,
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

	cutoff := time.Time{}
	if crit.Since > 0 {
		cutoff = uc.Clock.Now().Add(-crit.Since)
	}
	kept := make([]entities.Source, 0, len(srcs))
	for _, s := range srcs {
		if !cutoff.IsZero() && s.ImportedAt.Before(cutoff) {
			continue
		}
		if !sourceStateMatches(s, crit.State) {
			continue
		}
		kept = append(kept, s)
	}
	if crit.Text != "" {
		var note string
		kept, note = matchSourceWords(kept, crit.Text)
		if note != "" {
			cmd.Notes = append(cmd.Notes, note)
		}
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
		cmd.Notes = append(cmd.Notes, "no source matches that reading")
	}
	return nil
}

// resolveSourcesFromCounts turns "a set of sources, scored" into the command's materialized set.
// The three ways into it — a topic's claims, one claim's provenance, one named source — all end
// here, so the filtering, ordering and honest notes cannot drift apart.
func (uc RunCommand) resolveSourcesFromCounts(ctx context.Context, cmd *entities.Command, counts map[string]int) error {
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
	cmd.Vault = len(counts)
	cmd.Scanned = len(counts)
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
func (uc RunCommand) resolveSourcesByTopic(ctx context.Context, cmd *entities.Command) error {
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
	if err := uc.resolveSourcesFromCounts(ctx, cmd, counts); err != nil {
		return err
	}
	if len(atoms) > 0 {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("%d claims carry the topic %q", len(atoms), crit.FromTopic))
	}
	if cmd.Total == 0 {
		cmd.Notes = append(cmd.Notes,
			fmt.Sprintf("no source owns a claim about %q", crit.FromTopic))
	}
	return nil
}

// resolveSourcesByRefs answers a request that POINTS at something: one claim (its provenance is
// the answer) or one source (a set of exactly one, or the base the other criteria narrow).
func (uc RunCommand) resolveSourcesByRefs(ctx context.Context, cmd *entities.Command) error {
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

	if err := uc.resolveSourcesFromCounts(ctx, cmd, counts); err != nil {
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

// matchSourceWords narrows a source window by the request's words. ALL of them must appear in the
// fields a list row shows (title, original name, URL, tags); if no source has all of them, the
// ones that have at least one are kept and the relaxation is NAMED — never a silent "nothing".
func matchSourceWords(srcs []entities.Source, text string) ([]entities.Source, string) {
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return srcs, ""
	}
	has := func(s entities.Source, word string) bool {
		haystack := strings.ToLower(s.Title + " " + s.OriginalName + " " + s.URL + " " + strings.Join(s.Tags, " ") + " " + strings.Join(s.AutoTags, " "))
		return strings.Contains(haystack, word)
	}
	all := make([]entities.Source, 0, len(srcs))
	any := make([]entities.Source, 0, len(srcs))
	for _, s := range srcs {
		hits := 0
		for _, w := range words {
			if has(s, w) {
				hits++
			}
		}
		switch {
		case hits == len(words):
			all = append(all, s)
		case hits > 0:
			any = append(any, s)
		}
	}
	if len(all) > 0 {
		return all, ""
	}
	if len(any) > 0 {
		return any, fmt.Sprintf("no source has all of %s; these match at least one", strings.Join(words, ", "))
	}
	return nil, fmt.Sprintf("no source mentions any of %s", strings.Join(words, ", "))
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
	if crit.Since > 0 && src.ImportedAt.Before(time.Now().Add(-crit.Since)) {
		return false
	}
	if crit.Text != "" {
		needle := strings.ToLower(crit.Text)
		haystack := strings.ToLower(src.Title + " " + src.OriginalName + " " + src.URL + " " + strings.Join(src.Tags, " "))
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return sourceStateMatches(src, crit.State)
}

// resolveWiki answers "my own pages about X". Wiki pages are few and local, so the match is done
// here over title, tags and prose — the same "one word is enough, more words rank higher" ladder
// the lexical search uses, without an index that does not exist for them.
func (uc RunCommand) resolveWiki(ctx context.Context, cmd *entities.Command) error {
	if uc.Wiki == nil {
		return types.New("not_implemented", "the wiki is not configured", types.ErrNotImplemented)
	}
	pages, err := uc.Wiki.List(ctx)
	if err != nil {
		return err
	}
	crit := cmd.Criteria
	words := strings.Fields(strings.ToLower(crit.Text))

	type scored struct {
		page  entities.WikiPage
		score int
	}
	kept := make([]scored, 0, len(pages))
	var partial []scored
	for _, page := range pages {
		title := strings.ToLower(page.Title)
		body := strings.ToLower(page.Body)
		tags := make([]string, 0, len(page.Tags)+len(page.AutoTags))
		for _, tag := range append(append([]string{}, page.Tags...), page.AutoTags...) {
			tags = append(tags, strings.ToLower(tag))
		}
		if crit.Tag != "" && !slices.Contains(tags, strings.ToLower(crit.Tag)) {
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
	if relaxed {
		cmd.Notes = append(cmd.Notes,
			fmt.Sprintf("no page has all of %s; these match at least one", strings.Join(words, ", ")))
	}
	return nil
}

func (uc RunCommand) resolveClaims(ctx context.Context, cmd *entities.Command) error {
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
		hits, err := uc.Search.Search(ctx, crit.Text, "atoms", scan)
		if err != nil {
			return err
		}
		cmd.Scanned = len(hits)
		matched := 0
		for _, hit := range hits {
			if crit.Topic != "" && !uc.claimHasTopic(ctx, hit.ID, crit.Topic) {
				continue
			}
			matched++
			if len(items) >= uc.maxItems() {
				continue
			}
			items = append(items, entities.CommandItem{Ref: hit.ID, Title: hit.Title, Kind: "claim"})
		}
		cmd.Total = matched
	} else {
		atoms, err := uc.Atoms.ListAtoms(ctx, crit.Topic, scan, 0)
		if err != nil {
			return err
		}
		cmd.Scanned = len(atoms)
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
	}
	cmd.Items = items
	cmd.Vault = cmd.Scanned
	if cmd.Total == 0 {
		cmd.Notes = append(cmd.Notes, "no claim matches that reading")
	}
	return nil
}

// claimHasTopic re-reads one claim to check the topic. A lexical hit carries no topic, and a claim
// that does not carry the requested topic is not part of the set.
func (uc RunCommand) claimHasTopic(ctx context.Context, id, topic string) bool {
	parsed, err := ulid.ParseStrict(id)
	if err != nil {
		return false
	}
	atom, err := uc.Atoms.GetAtom(ctx, parsed)
	if err != nil {
		return false
	}
	return strings.EqualFold(atom.Topic, topic)
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

func (uc RunCommand) maxItems() int {
	if uc.MaxItems > 0 {
		return uc.MaxItems
	}
	return defaultCommandItems
}

func (uc RunCommand) scanLimit() int {
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
			Text: c.Criteria.Text, Tag: c.Criteria.Tag, Kind: c.Criteria.Kind,
			Topic: c.Criteria.Topic, FromTopic: c.Criteria.FromTopic,
			Tags: c.Criteria.Tags, Topics: c.Criteria.Topics,
			Source: c.Criteria.Source, Atom: c.Criteria.Atom,
			Since: c.Criteria.Since, State: string(c.Criteria.State),
			Order: string(c.Criteria.Order), BookmarksOnly: c.Criteria.BookmarksOnly,
			AttachedOnly: c.Criteria.AttachedOnly,
		},
	}
}
