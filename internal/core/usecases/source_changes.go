package usecases

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/textdiff"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// changesFence surrounds the diff. Four backticks, so a page whose own text contains a fence cannot
// close the block early.
const changesFence = "````"

// materialChange says whether a refresh is worth an EVENT: a snapshot the vault replaces, an outline
// rebuild, a change record, a moved change time and a cluster pass. The snapshot a source holds is
// the baseline of its derivation — the outline's references are byte ranges into it — so a change
// below the threshold is not written at all: the bytes stay, the drift accumulates against them, and
// a page that gains a sentence a week is re-derived once it has gained a paragraph (see
// docs/adr/0003-the-snapshot-moves-only-on-an-event.md).
//
// The measurement is a WORD SHIFT against the STORED body, so it accumulates: the title moved, or at
// least requiredWords word positions differ and those words average more than ChangeMinWordLength
// characters. A ticking timestamp, a view counter, a typo or a reflow is not an event. Zero words
// turns the rule off: any difference in the body is material.
func (uc URLs) materialChange(oldTitle, newTitle, oldBody, newBody string, bodyDiffers bool) (bool, textdiff.WordShift) {
	if oldTitle != newTitle {
		return true, textdiff.WordShift{}
	}
	if !bodyDiffers {
		return false, textdiff.WordShift{}
	}
	shift := textdiff.Words(oldBody, newBody)
	if uc.ChangeMinWords <= 0 {
		return true, shift
	}
	if shift.Words < uc.requiredWords(shift.BaseWords) || shift.AvgRunes <= float64(uc.ChangeMinWordLength) {
		return false, shift
	}
	return true, shift
}

// requiredWords is how many word positions a change of a body this size must move to be an event: the
// absolute floor (ChangeMinWords), raised by the share rule (ChangeMinPercent of the page) when that
// asks for more. A long page must therefore change more than a short one before it is worth a model
// call, and a short page is still governed by the floor. Zero means the rule is off.
func (uc URLs) requiredWords(baseWords int) int {
	if uc.ChangeMinWords <= 0 {
		return 0
	}
	need := uc.ChangeMinWords
	if uc.ChangeMinPercent > 0 {
		// Round UP: a share of 1 % of 1 500 words is 15, and 0.9 of a word is still a word.
		if share := (baseWords*uc.ChangeMinPercent + 99) / 100; share > need {
			need = share
		}
	}
	return need
}

// writeChangeRecord writes a source's <hash>.changes.md: the LAST change of its page as a reader can
// read it — when, the title before and after, and a unified diff of the readable body.
//
// It runs on the paths that rewrite a URL snapshot in place, while BOTH bodies are in hand: the
// refresh already reads the old one to decide whether the outline has to be rebuilt, so the record
// costs no extra I/O and keeps no second copy of the page. A first read is an arrival, not a change,
// and writes nothing.
//
// A failure is a warning, never a failed refresh: the record describes a write that already
// happened, and a full disk must not turn a successful refresh into an error.
func writeChangeRecord(store interfaces.SourceStore, log *slog.Logger, hash, oldTitle, newTitle, oldBody, newBody string, sameBody bool, shift textdiff.WordShift, at time.Time) {
	if store == nil || hash == "" {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Changed %s\n\n", at.UTC().Format(time.RFC3339))
	if oldTitle != newTitle {
		fmt.Fprintf(&b, "Title: %s → %s\n\n", quoteTitle(oldTitle), quoteTitle(newTitle))
	}
	if sameBody {
		b.WriteString("Body: unchanged.\n")
	} else {
		diff := textdiff.Lines(oldBody, newBody)
		switch {
		case diff.Added == 0 && diff.Removed == 0 && diff.Text == "":
			b.WriteString("Body: changed only in whitespace or line endings.\n")
		default:
			fmt.Fprintf(&b, "Body: +%d -%d lines.\n", diff.Added, diff.Removed)
			// The measurement that made this change an event, so the threshold stays tunable instead
			// of being a rule nobody can see.
			fmt.Fprintf(&b, "Words: %d changed, %.1f characters on average.\n\n", shift.Words, shift.AvgRunes)
			fmt.Fprintf(&b, "%sdiff\n%s%s\n", changesFence, diff.Text, changesFence)
			if diff.Truncated {
				b.WriteString("\nThe diff stops early; the snapshot holds the full text.\n")
			}
		}
	}
	if err := store.PutRendered(hash, entities.SidecarChanges, []byte(b.String())); err != nil && log != nil {
		log.Warn("source change record not written", "hash", hash, "err", err)
	}
}

// quoteTitle puts a title on ONE line of the record. A title with a line break or a stray quote is
// still a title, not a way to break the file.
func quoteTitle(title string) string {
	clean := strings.Join(strings.Fields(title), " ")
	clean = strings.ReplaceAll(clean, `"`, `'`)
	return `"` + clean + `"`
}
