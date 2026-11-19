package app

import (
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// The update path's pace. Every automatic source — a due refresh, a page a sitemap, feed or listing
// declared, a wiki page's link, a feed's own copy, an inbox drop — is TAKEN IN at most once per
// capture.refresh_min_gap. Taking a source in pays for its fetch AND its document steps (capture,
// outline, distill, including the continuation runs of a long document); the next source waits for
// the next turn. A human's own action — an add, a paste, the Refresh button, a run, a reset, a
// manual capture — is not paced and does not spend the turn: it is simply booked.
//
// Two callers share the turn: the fetch worker (one automatic fetch) and the pump (a source that has
// no fetch of its own). The pump never blocks on it — the sources already taken in still need their
// claims — it asks for another pass when the turn is free.

// admissionGap is the pace of the update path: empty or "0s" is the burst. The config validator
// refuses anything that is not a duration, so a value that cannot be parsed here comes from a broken
// file and is read as the burst.
func (a *App) admissionGap() time.Duration {
	raw := strings.TrimSpace(a.conf().Capture.RefreshMinGap)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// awaitAdmission waits for the next automatic turn and takes it. It returns the instant the turn
// replaced, so an idle caller can give it back, and false when the app quits.
func (a *App) awaitAdmission() (time.Time, bool) {
	for {
		gap := a.admissionGap()
		if gap <= 0 {
			return time.Time{}, true
		}
		a.admissionMu.Lock()
		wait := time.Duration(0)
		if !a.admissionAt.IsZero() {
			wait = gap - time.Since(a.admissionAt)
		}
		if wait <= 0 {
			prev := a.admissionAt
			a.admissionAt = time.Now()
			a.admissionMu.Unlock()
			return prev, true
		}
		a.admissionMu.Unlock()
		if !a.waitFor(wait) {
			return time.Time{}, false
		}
	}
}

// takeAdmission takes the turn only if it is free. The pump uses it: a pass that finds the turn
// spent skips the source and asks for another pass, instead of holding the sources it has already.
func (a *App) takeAdmission() bool {
	gap := a.admissionGap()
	if gap <= 0 {
		return true
	}
	a.admissionMu.Lock()
	defer a.admissionMu.Unlock()
	if !a.admissionAt.IsZero() && time.Since(a.admissionAt) < gap {
		return false
	}
	a.admissionAt = time.Now()
	return true
}

// releaseAdmission gives back a turn that fetched nothing, so an idle look does not cost the update
// path a gap. Nobody else can have taken the turn meanwhile: a fresh stamp holds it until the gap.
func (a *App) releaseAdmission(prev time.Time) {
	a.admissionMu.Lock()
	a.admissionAt = prev
	a.admissionMu.Unlock()
	// A source may be waiting for exactly this turn: wake the pump now instead of letting it sleep
	// out the wait it was told a moment ago.
	a.pumpPipeline("the update turn came back")
}

// admissionWait is how long until the next turn.
func (a *App) admissionWait() time.Duration {
	gap := a.admissionGap()
	if gap <= 0 {
		return 0
	}
	a.admissionMu.Lock()
	defer a.admissionMu.Unlock()
	if a.admissionAt.IsZero() {
		return 0
	}
	if wait := gap - time.Since(a.admissionAt); wait > 0 {
		return wait
	}
	return 0
}

// bookAdmission records that this source's turn is already paid for: its automatic fetch took it, or
// a human action is not paced at all. The pump may work on a booked source without waiting.
func (a *App) bookAdmission(hash string) {
	// Burst: nothing is paced, so there is nothing to remember, and a big import does not grow a
	// ledger for sources that were never limited.
	if hash == "" || a.admissionGap() <= 0 {
		return
	}
	a.admissionMu.Lock()
	if a.admitted == nil {
		a.admitted = map[string]bool{}
	}
	a.admitted[hash] = true
	a.admissionMu.Unlock()
}

// admissionBooked reports whether this source's turn is already paid for.
func (a *App) admissionBooked(hash string) bool {
	a.admissionMu.Lock()
	defer a.admissionMu.Unlock()
	return a.admitted[hash]
}

// admitSourceForStep books a source into the update path if it is not in yet and reports whether the
// pump may claim it NOW. When the turn is spent the source is skipped and the pass is marked for a
// later retry: the sources already taken in keep their claims either way.
func (a *App) admitSourceForStep(hash string, deferred *bool) bool {
	if a.admissionBooked(hash) {
		return true
	}
	if a.admissionGap() > 0 {
		if !a.takeAdmission() {
			if !*deferred {
				*deferred = true
				a.pumpLater(a.admissionWait())
			}
			return false
		}
	}
	a.bookAdmission(hash)
	return true
}

// forgetFinishedAdmissions drops the sources whose three document steps are through: their update
// path is over, so a later event (a refresh, a repair that re-opens a step) takes a fresh turn. A
// source that is gone, or that the index does not know, leaves with them.
func (a *App) forgetFinishedAdmissions(records map[string]entities.SourceRecord, known map[string]bool) {
	a.admissionMu.Lock()
	defer a.admissionMu.Unlock()
	for hash := range a.admitted {
		rec, ok := records[hash]
		if !ok || (len(known) > 0 && !known[hash]) {
			delete(a.admitted, hash)
			continue
		}
		if a.stepThrough(hash, rec, entities.StepCapture) &&
			a.stepThrough(hash, rec, entities.StepOutline) &&
			a.stepThrough(hash, rec, entities.StepDistill) {
			delete(a.admitted, hash)
		}
	}
}

// pumpLater asks for a pipeline pass once the automatic turn is free. It must not block the pump:
// the sources already taken in still owe claims, and the pass that waits would claim nothing. At
// most ONE retry is armed — later passes have nothing to add — and a turn that comes back early
// wakes the pump through releaseAdmission instead of waiting out the timer.
func (a *App) pumpLater(d time.Duration) {
	if d <= 0 {
		a.pumpPipeline("the next update turn")
		return
	}
	at := time.Now().Add(d)
	a.pumpTurnMu.Lock()
	if !a.pumpTurnAt.IsZero() {
		a.pumpTurnMu.Unlock()
		return // a retry is already armed
	}
	a.pumpTurnAt = at
	a.pumpTurnMu.Unlock()
	time.AfterFunc(d, func() {
		a.pumpTurnMu.Lock()
		a.pumpTurnAt = time.Time{}
		a.pumpTurnMu.Unlock()
		a.pumpPipeline("the next update turn")
	})
}

// waitFor sleeps for d and reports false when the app quits first.
func (a *App) waitFor(d time.Duration) bool {
	if d < time.Millisecond {
		d = time.Millisecond
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-a.quit:
		return false
	case <-timer.C:
		return true
	}
}
