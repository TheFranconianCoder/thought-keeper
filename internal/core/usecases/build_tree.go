package usecases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// BuildTree extracts a source into parts, shapes them into a tree and persists
// both the node rows and the tree sidecar.
type BuildTree struct {
	// SourceLog records extraction and tree building where the source lives.
	SourceLog interfaces.SourceLog
	Clock     interfaces.Clock
	// Gateway names the document: the title comes from the content, not from a heading.
	Gateway   interfaces.AgentGateway
	Model     string
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Trees     interfaces.TreeStore
	Extractor interfaces.Extractor
	// Clean removes the claims a rebuilt tree INVALIDATED — the ones whose parts changed — and keeps
	// the rest: atoms cite tree NODES, so a node that is still there with the same text still means
	// what its claims say. An unchanged rebuild (the common repair) removes nothing.
	Clean CleanSource
	// Parts re-arms the parts a rebuild changed: their "done" record described text that is gone.
	Parts interfaces.PartRepository
	// Steps records this step's state (outline: running/ok/failed) for the source's lights.
	Steps interfaces.StepTracker
	// Languages recognises the source's language from the readable parts (or, for code-heavy text,
	// from the outline's own titles and summaries). Nil means no detection.
	Languages interfaces.LanguageDetector
}

// TreeResult summarizes a tree build.
type TreeResult struct {
	Hash  string
	Nodes int
	Pages int
	Title string
	OCR   bool
	// Reopened is how many text-bearing parts a changed tree re-armed (their record described text
	// that is gone, or the node is new). A caller that wants the changed parts derived again must
	// look at this, not at "does the source have no claims": a source with unchanged parts already
	// has claims, and the usual "no atoms" trigger would skip exactly those parts.
	Reopened int
}

// Execute builds the tree for one source hash and records the OUTLINE step's state: this is the
// step that turns a captured source into a structure, and the step after it (distill) may only run
// on a tree that is ok.
func (uc BuildTree) Execute(ctx context.Context, hash string) (TreeResult, error) {
	// Nothing to outline without a readable capture. A bookmark is metadata by definition, and a
	// blocked URL's snapshot is a note about the GATE — a tree built from it would look like knowledge
	// about the page, and the record already says the step does not apply. A feed or a sitemap is a
	// LIST of sources: its outline is the list it already is, and a tree built from it would derive
	// claims about the list instead of the pages it owns.
	if src, err := uc.Index.GetSourceByHash(ctx, hash); err == nil {
		if src.Bookmark || src.Steps.State(entities.StepCapture) == entities.StepFailed || src.OwnsChildren() {
			if src.OwnsChildren() {
				what := "feed"
				if src.Sitemap {
					what = "sitemap"
				}
				recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepOutline, entities.StepSkipped, what, nil)
				recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepDistill, entities.StepSkipped, what, nil)
			}
			return TreeResult{Hash: hash}, nil
		}
	}
	recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepOutline, entities.StepRunning, "", nil)
	res, err := uc.execute(ctx, hash)
	if err != nil {
		// A failed outline blocks distill: an atom cites a tree node, and a source without a
		// tree has no node to cite.
		recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepOutline, entities.StepFailed, "", err)
		return res, err
	}
	recordStep(ctx, uc.Steps, uc.Clock, hash, entities.StepOutline, entities.StepOK,
		fmt.Sprintf("%d nodes · %d pages", res.Nodes, res.Pages), nil)
	return res, nil
}

func (uc BuildTree) execute(ctx context.Context, hash string) (TreeResult, error) {
	src, err := uc.Index.GetSourceByHash(ctx, hash)
	if err != nil {
		return TreeResult{}, err
	}
	_ = uc.Index.SetSourceTree(ctx, hash, string(entities.TreePending), "", 0)

	// OUTLINE READS. Step 1 (capture) wrote the source's readable rendering
	// (sources/<ab>/<cd>/<hash>.md, for a kind that has one); parsing it produces exactly the parts the bytes
	// produced, so a rebuild cannot disagree with the derivation it is supposed to keep. Nothing is
	// extracted here — no poppler, no tesseract, nothing that could drift.
	parts, fromRender, err := uc.parts(ctx, hash, src)
	if err != nil {
		_ = uc.Index.SetSourceTree(ctx, hash, string(entities.TreeFailed), "", 0)
		return TreeResult{}, err
	}
	chars := 0
	for _, sec := range parts {
		chars += len(sec.Text)
	}
	nodes := tree.Build(parts)
	pages, ocr := summarize(parts)
	uc.logSource(ctx, hash, "info", "outline", "parts read", map[string]any{
		"kind": src.Kind, "pages": pages, "chars": chars, "ocr": ocr,
		"from": map[bool]string{true: "capture rendering", false: "the object itself"}[fromRender],
	})
	// What the tree leaves out is named: front/back matter (acknowledgements, dedication, index) and
	// site chrome never become nodes, and "the tree has 40 nodes" alone would hide that.
	if dropped := tree.DroppedTitles(parts); len(dropped) > 0 {
		shown := dropped
		if len(shown) > 6 {
			shown = shown[:6]
		}
		uc.logSource(ctx, hash, "info", "outline", "dropped parts", map[string]any{
			"parts": len(dropped), "titles": strings.Join(shown, ", "),
			"more": len(dropped) - len(shown),
		})
	}

	treeStart := uc.Clock.Now()
	depth := 0
	for _, n := range nodes {
		if n.Depth > depth {
			depth = n.Depth
		}
	}
	uc.logSource(ctx, hash, "info", "outline", "tree built", map[string]any{
		"nodes": len(nodes) - 1, "depth": depth, "pages": pages,
		"dur": uc.Clock.Now().Sub(treeStart).Round(time.Millisecond).String(),
	})
	title, titleSource := uc.documentTitle(ctx, src, parts)
	uc.logSource(ctx, hash, "info", "title", "document title determined", map[string]any{
		"title": title, "source": titleSource, "original": src.OriginalName, "url": src.URL,
	})
	// The title lives next to the source (.meta) as well as in the index: the outline may REFINE
	// what step 1 determined (the model sees the whole outline), and the file is what a reindex
	// restores it from.
	if uc.Steps != nil {
		if err := uc.Steps.SetTitle(hash, title); err != nil {
			uc.logSource(ctx, hash, "warn", "title", "could not record the title", map[string]any{"err": err})
		}
	}
	// The outline reads the whole document, so it is the second place — after intake/capture — where
	// a language hint can be determined. This matters for sources written before the field existed
	// and for code-heavy Markdown whose raw text defeats the trigram detector: the outline's titles
	// and summaries are natural language even when the body is mostly code.
	if src.Language == "" {
		if language := uc.detectLanguage(src, parts, nodes, title); language != "" {
			src.Language = language
			if uc.Steps != nil {
				_ = uc.Steps.SetLanguage(hash, language)
			} else if uc.Index != nil {
				_ = uc.Index.SetSourceLanguage(ctx, hash, language)
			}
			uc.logSource(ctx, hash, "info", "language", "source language detected", map[string]any{"language": language})
		}
	}

	// Was the tree different before? Only then is the derivation stale. Node ids are the
	// address atoms cite, so a changed tree invalidates every atom of this source.
	// The sidecar holds the previous tree INCLUDING the node text — the exact thing the atoms
	// were derived from. (The index rows keep only a hash of it.)
	previous, _ := uc.Trees.Read(ctx, hash)
	changed := treeChanged(previous, nodes)
	reopened := 0

	if err := uc.Index.ReplaceSourceNodes(ctx, src.ID, nodes); err != nil {
		_ = uc.Index.SetSourceTree(ctx, hash, string(entities.TreeFailed), "", 0)
		return TreeResult{}, err
	}
	if err := uc.Trees.Write(ctx, hash, nodes); err != nil {
		_ = uc.Index.SetSourceTree(ctx, hash, string(entities.TreeFailed), "", 0)
		return TreeResult{}, err
	}
	if err := uc.Index.SetSourceTree(ctx, hash, string(entities.TreeBuilt), title, pages); err != nil {
		return TreeResult{}, err
	}
	if changed {
		// A rebuild changed the tree. What that means depends on WHAT changed: a node whose id AND
		// text are the same is still the part a claim cites, derived from the text it was derived
		// from — so its claims are still true and stay. Everything else (a node that is new, whose
		// text changed, or that is gone) loses its claims and is derived again. The pages that carried
		// them are MARKED and step 4 repairs them.
		//
		// This is the difference between "the page changed" and "the page is new". Invalidating the
		// whole derivation was the old answer, and it made a corrected paragraph cost every claim of a
		// document with hundreds of parts.
		keep := unchangedNodes(previous, nodes)
		cleaned, err := uc.Clean.Correct(ctx, hash, keep)
		if err != nil {
			uc.logSource(ctx, hash, "warn", "tree", "correcting the old derivation failed", map[string]any{"err": err})
		} else {
			uc.logSource(ctx, hash, "warn", "tree", "tree changed — derivation corrected", map[string]any{
				"atoms_removed": cleaned.Atoms,
				"parts_kept":    len(keep), "parts_total": len(nodes),
			})
		}
		reopened = uc.rearmParts(ctx, hash, src, nodes, keep)
	}
	return TreeResult{Hash: hash, Nodes: len(nodes) - 1, Pages: pages, Title: title, OCR: ocr, Reopened: reopened}, nil
}

// parts reads the parts the outline works from: the capture rendering when there is one
// (step 1 wrote it), otherwise the object itself — which is the readable form for Markdown, text and
// a URL snapshot. The fallback is for a vault written before the step existed, and it says so in the
// log: the normal path extracts nothing.
func (uc BuildTree) parts(ctx context.Context, hash string, src entities.Source) ([]entities.Part, bool, error) {
	// ONLY a source whose bytes are not readable has a rendering. For a Markdown source the rendering
	// IS the object (<hash>.md) — reading it here would parse the raw document as one untitled
	// part instead of extracting its headings.
	if src.NeedsCapture() {
		if rc, err := uc.Store.OpenRendered(hash, entities.SidecarRender); err == nil {
			defer func() { _ = rc.Close() }()
			if b, rErr := io.ReadAll(rc); rErr == nil {
				if parts := tree.ParseRendering(string(b)); len(parts) > 0 {
					return parts, true, nil
				}
			}
		}
	}

	if uc.Extractor == nil {
		// A wiring mistake must not panic a worker (it would take the whole app down): say what is
		// missing instead.
		return nil, false, errors.New("outline: no extractor configured and no capture rendering to read")
	}
	rc, err := uc.Store.Open(hash, src.Ext)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rc.Close() }()
	// The object is read once: the extractor gets it as a stream, and the same text is what the
	// parts are located in (a reference is an offset into the file the outline points at).
	objectBytes, err := io.ReadAll(rc)
	if err != nil {
		return nil, false, err
	}
	objectText := string(objectBytes)
	uc.logSource(ctx, hash, "info", "outline", "no capture rendering — reading the object", map[string]any{
		"kind": src.Kind,
	})
	onExtract := func(ev entities.ExtractEvent) {
		level, msg := "info", "ocr page"
		fields := map[string]any{"page": ev.Page, "chars": ev.Chars}
		switch {
		case ev.Kind == "furniture":
			uc.logSource(ctx, hash, "info", "furniture", "dropped page furniture", map[string]any{
				"lines": ev.Lines, "pages": ev.Pages,
			})
			return
		case ev.Err != nil:
			level, msg, fields["err"] = "warn", "ocr failed", ev.Err
		case ev.Chars == 0:
			level, msg = "warn", "ocr returned no text"
		}
		uc.logSource(ctx, hash, level, "ocr", msg, fields)
	}
	parts, err := uc.Extractor.Extract(ctx, src.Ext, objectBytes, onExtract)
	if err != nil {
		return nil, false, err
	}
	return uc.locate(ctx, hash, objectText, parts), false, nil
}

// locate gives every part its REFERENCE into the text it was read from: the outline stores where a
// node's text is, not the text itself. It is one forward scan (the parts are in document order) and
// it says how many texts it could not find — an extractor that normalised its text, or a source whose
// readable form is not the object it was extracted from.
func (uc BuildTree) locate(ctx context.Context, hash, text string, parts []entities.Part) []entities.Part {
	unlocated := tree.Locate(text, parts)
	if unlocated > 0 {
		uc.logSource(ctx, hash, "warn", "outline", "text not found in the readable text", map[string]any{
			"nodes": unlocated, "of": len(parts),
		})
	}
	return parts
}

// unchangedNodes is the set of node ids whose text is the same as before the rebuild: the address a
// claim cites and the text it was derived from are both unchanged, so those claims are still true.
// A node that is new, whose text changed, or that is gone is absent from the set.
func unchangedNodes(previous, rebuilt []entities.SourceNode) map[string]bool {
	before := make(map[string]string, len(previous))
	for _, n := range previous {
		before[n.NodeID] = n.TextHash
	}
	keep := make(map[string]bool, len(rebuilt))
	for _, n := range rebuilt {
		// Only a text-bearing node is a part, and only a part carries claims.
		if n.TextHash != "" && before[n.NodeID] == n.TextHash {
			keep[n.NodeID] = true
		}
	}
	return keep
}

// rearmParts marks the parts a rebuild CHANGED as open work. Their record says "done" about text that
// is not there any more, and a done part is skipped on the next run — so it must be derived again. The
// parts that did not change keep their record (and are not derived again).
func (uc BuildTree) rearmParts(ctx context.Context, hash string, src entities.Source, nodes []entities.SourceNode, keep map[string]bool) int {
	if uc.Parts == nil {
		return 0
	}
	reopened := 0
	for _, n := range nodes {
		if n.TextHash == "" || keep[n.NodeID] {
			continue
		}
		if err := uc.Parts.SetPartState(ctx, src.ID, entities.PartState{
			NodeID: n.NodeID, State: entities.PartIncomplete, Reason: "tree_changed",
			UpdatedAt: uc.Clock.Now(),
		}); err != nil {
			uc.logSource(ctx, hash, "warn", "tree", "could not re-arm a changed part", map[string]any{"err": err})
			continue
		}
		reopened++
	}
	return reopened
}

// treeChanged reports whether a rebuilt tree differs from the previous one. Compared by node id
// (the address atoms cite), title and node text: an extractor or filter change that moves nodes —
// or that drops them, as the boilerplate filter does — makes the old provenance point at the
// wrong part, so the derivation built on it must go.
func treeChanged(previous, rebuilt []entities.SourceNode) bool {
	if len(previous) == 0 {
		return false // a first build invalidates nothing
	}
	if len(previous) != len(rebuilt) {
		return true
	}
	for i := range previous {
		// The TEXT is compared as its HASH: the outline does not carry the text, and the hash is the
		// same fact in 64 characters instead of a whole document.
		if previous[i].NodeID != rebuilt[i].NodeID ||
			previous[i].Title != rebuilt[i].Title ||
			previous[i].TextHash != rebuilt[i].TextHash {
			return true
		}
	}
	return false
}

// documentTitle names the document. The title belongs to the content, so the model decides it;
// the raw input (a URL's declared title, the first heading, the file name) is only the fallback.
func (uc BuildTree) documentTitle(ctx context.Context, src entities.Source, parts []entities.Part) (string, string) {
	// A wiki page's title is the page's own, never the model's: the human named it, and the source
	// is only a representation of that page.
	if src.IsWiki() {
		return DocumentTitle(src, parts, "")
	}
	if uc.Gateway != nil && uc.Model != "" {
		if t := uc.modelTitle(ctx, src, parts); t != "" {
			return t, "model"
		}
	}
	// No model (or it failed): the deterministic chain of step 1 decides.
	return DocumentTitle(src, parts, "")
}

// modelTitle asks the model for one line: the document's title.
func (uc BuildTree) modelTitle(ctx context.Context, src entities.Source, parts []entities.Part) string {
	var b strings.Builder
	b.WriteString("Outline:\n")
	b.WriteString(oneLine(outlineOfSourceNodes(parts), 1200))
	b.WriteString("\n\nBeginning of the document:\n")
	limit := 4000
	for _, sec := range parts {
		if limit <= 0 {
			break
		}
		text := sec.Text
		if len(text) > limit {
			text = text[:limit]
		}
		limit -= len(text)
		b.WriteString(text)
		b.WriteString("\n")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	resp, err := uc.Gateway.Complete(ctx, interfaces.AgentRequest{
		Model:   uc.Model,
		OnRetry: retryNotice(ctx, uc.SourceLog, uc.Clock, src.Hash, "title", ""),
		Messages: []interfaces.AgentMessage{
			{Role: "system", Content: "You name documents. Answer with the title only: one line, no quotes, no trailing punctuation, at most 120 characters."},
			{Role: "user", Content: src.OriginalName + "\n" + b.String()},
		},
	})
	if err != nil {
		if uc.SourceLog != nil {
			uc.logSource(ctx, src.Hash, "warn", "title", "model title failed, falling back", map[string]any{"err": err})
		}
		return ""
	}
	title := strings.TrimSpace(strings.SplitN(resp.Message.Content, "\n", 2)[0])
	title = strings.Trim(title, "\"'` ")
	if len(title) > 200 {
		title = title[:200]
	}
	if len([]rune(title)) < 3 {
		return ""
	}
	return title
}

// outlineOfSourceNodes renders part titles for the title prompt.
func outlineOfSourceNodes(parts []entities.Part) string {
	var b strings.Builder
	for _, s := range parts {
		if t := strings.TrimSpace(s.Title); t != "" && !isPageTitle(t) {
			b.WriteString("- " + t + "\n")
		}
	}
	return b.String()
}

// firstHeading returns the first real heading (page titles excluded).
// logSource records one source-scoped event (no-op when no log is configured).
func (uc BuildTree) logSource(ctx context.Context, hash, level, phase, msg string, fields map[string]any) {
	if uc.SourceLog == nil || hash == "" {
		return
	}
	uc.SourceLog.Append(ctx, hash, interfaces.SourceLogEvent{
		At: uc.Clock.Now(), Phase: phase, Level: level, Message: msg, Fields: fields,
	})
}

// detectLanguage tries the readable text first, then the outline's natural-language titles and
// summaries. The fallback is what gives a code-heavy Markdown source a language: its raw text is
// mostly symbols, while its outline says what the document is about.
func (uc BuildTree) detectLanguage(src entities.Source, parts []entities.Part, nodes []entities.SourceNode, title string) string {
	if uc.Languages == nil {
		return ""
	}
	var sample strings.Builder
	for _, part := range parts {
		if sample.Len() >= 128<<10 {
			break
		}
		sample.WriteString(part.Text)
		sample.WriteByte('\n')
	}
	if language := uc.Languages.Detect(sample.String()); language != "" {
		return language
	}
	sample.Reset()
	for _, node := range nodes {
		if sample.Len() >= 32<<10 {
			break
		}
		sample.WriteString(node.Title)
		sample.WriteString(". ")
		sample.WriteString(node.Summary)
		sample.WriteByte('\n')
	}
	if sample.Len() == 0 {
		sample.WriteString(title)
	}
	return uc.Languages.Detect(sample.String())
}

func summarize(parts []entities.Part) (pages int, ocr bool) {
	for _, s := range parts {
		if s.Page > pages {
			pages = s.Page
		}
		if s.OCR {
			ocr = true
		}
	}
	return pages, ocr
}
