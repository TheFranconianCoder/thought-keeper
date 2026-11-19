package app

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/modelwindow"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// The derivation's model windows. `openai.blocked_windows` names the UTC ranges in which the app's
// DERIVATION must not call the model: the outline's naming, distill, the summary, the auto tags and
// the vocabulary verbs. The scheduler refuses to claim or queue that work inside a window, one wake
// is armed for the opening, and a call still in flight at the boundary is cut there (the gateway's
// guard) — the work is kept and resumes afterwards. Ask and the assistant are interactive reads and
// keep running; so do capture (fetch, extract, OCR) and the cluster step, which has no model.
//
// The config is read on every question instead of being cached: the window is one knob, and a change
// in the Config overlay must apply to the next decision, not to the next process.

// modelWindows parses the running configuration's windows. A value the parser cannot read is no
// window at all: the config is the broken part, and a typo must not silently stop the derivation.
// (Write and ReloadConfig validate the file; a hand-edited file is read defensively here.)
func (a *App) modelWindows() modelwindow.Windows {
	windows, err := modelwindow.Parse(a.conf().OpenAI.BlockedWindows)
	if err != nil {
		return modelwindow.Windows{}
	}
	return windows
}

// modelWindowClosed reports whether the derivation may not call the model now, and when the window
// opens again (the zero instant when it is open).
func (a *App) modelWindowClosed() (bool, time.Time) {
	return a.modelWindows().ClosedAt(time.Now())
}

// modelWindowReason is the sentence a refusal, a log line and a run row share.
func modelWindowReason(reopen time.Time) string {
	return "the model window is closed until " + reopen.UTC().Format(time.RFC3339)
}

// refusal is the wire error of a manual action that would start derivation inside a window.
func modelWindowRefusal(reopen time.Time) error {
	return types.New("conflict", "the derivation is paused: "+modelWindowReason(reopen), types.ErrConflict)
}

// modelWindowPause arms ONE wake for the instant the window opens. It does not poll: the derivation
// is resumed once, by the pass the wake asks for. A wake at or before the armed instant wins; a later
// one would only duplicate it.
func (a *App) modelWindowPause(until time.Time) {
	if until.IsZero() {
		return
	}
	wait := max(time.Until(until), time.Millisecond)
	a.windowMu.Lock()
	if !a.windowWakeAt.IsZero() && !a.windowWakeAt.After(until) {
		a.windowMu.Unlock()
		return
	}
	a.windowWakeAt = until
	a.windowMu.Unlock()
	// The Status lens reads the window from the pipeline frame; a pass that just learnt it must say so.
	a.publishPipeline()
	time.AfterFunc(wait, func() {
		a.windowMu.Lock()
		armed := a.windowWakeAt.Equal(until)
		if armed {
			a.windowWakeAt = time.Time{}
		}
		a.windowMu.Unlock()
		if !armed {
			return // a later wake was armed for this window; this timer is stale
		}
		if closed, reopen := a.modelWindowClosed(); closed {
			a.modelWindowPause(reopen)
			return
		}
		a.resumeDerivation()
	})
}

// resumeDerivation is what "the window is over" means: one pipeline pass, the summary and tag
// backfills topped up, the vocabulary check, and the wiki pages whose tagging was deferred.
func (a *App) resumeDerivation() {
	a.log.Info("model window closed — the derivation resumes",
		"windows", a.modelWindows().Strings())
	a.publishPipeline()
	a.pumpPipeline("the model window opened")
	a.topUpSummaries()
	a.topUpSourceTags()
	a.requestVocabularyIfDue("the model window opened")
	a.resumeWikiTags()
}

// rememberWikiTag keeps a page whose tag run could not start: a wiki page has no backlog to walk, so
// the wake needs the page's id.
func (a *App) rememberWikiTag(pageID string) {
	if pageID == "" {
		return
	}
	a.windowMu.Lock()
	if a.wikiTags == nil {
		a.wikiTags = map[string]bool{}
	}
	a.wikiTags[pageID] = true
	a.windowMu.Unlock()
}

// forgetWikiTag drops a page whose tag run has started.
func (a *App) forgetWikiTag(pageID string) {
	a.windowMu.Lock()
	delete(a.wikiTags, pageID)
	a.windowMu.Unlock()
}

// resumeWikiTags starts the tagging a window held back.
func (a *App) resumeWikiTags() {
	a.windowMu.Lock()
	pages := make([]string, 0, len(a.wikiTags))
	for page := range a.wikiTags {
		pages = append(pages, page)
	}
	clear(a.wikiTags)
	a.windowMu.Unlock()
	for _, page := range pages {
		a.ScheduleWikiTag(page)
	}
}

// jobNeedsModel reports whether a run is DERIVATION: its calls go through the windowed gateway, so it
// must not start inside a window. Queries and lint are reads; they are never held.
func jobNeedsModel(job runJob) bool {
	switch job.op {
	case entities.RunPipeline, entities.RunSummary, entities.RunTag, entities.RunVocabulary:
		return true
	}
	return false
}

// deferRun closes a run a window kept from starting, hands its claim and its backlog place back, and
// arms the wake that will ask again. It never deletes derived work: a distill that was cut keeps its
// finished parts.
func (a *App) deferRun(job runJob, reopen time.Time) {
	reason := "deferred: " + modelWindowReason(reopen)
	switch job.op {
	case entities.RunPipeline:
		a.releaseDistill(job.hash)
	case entities.RunSummary:
		// Keep the source in the summary backlog: it is owed an overview, not failed for one.
		a.releaseSummary(job.hash)
		a.mergeSummaryMissing([]string{job.hash})
	case entities.RunTag:
		if job.hash != "" {
			a.releaseSourceTag(job.hash)
			a.mergeSourceTagMissing([]string{job.hash})
		} else if job.page != (ulid.ULID{}) {
			a.rememberWikiTag(job.page.String())
		}
	case entities.RunVocabulary:
		a.vocabularyDeferred()
	}
	if job.runID != (ulid.ULID{}) {
		_ = a.deps().StartRun.Abandon(context.Background(), job.runID, reason) //nolint:errcheck // the run row is an audit trail; the work already ended
		a.clearRun(job.runID)
	}
	a.log.Info("derivation deferred: the model window is closed",
		"op", string(job.op), "target", job.hash, "until", reopen.UTC().Format(time.RFC3339))
	a.modelWindowPause(reopen)
}

// vocabularyDeferred clears the automatic curation's one-at-a-time flag and its cooldown, so the pass
// the window interrupted is asked again as soon as the window opens.
func (a *App) vocabularyDeferred() {
	a.vocabMu.Lock()
	a.vocabActive = false
	a.vocabLast = time.Time{}
	a.vocabMu.Unlock()
}

// windowDeferralOf is the app's view of the gateway's sentinel: a call that was cut by a window is a
// deferral, not a failure, wherever it happened.
func windowDeferralOf(err error) (time.Time, bool) {
	return interfaces.WindowReopen(err)
}
