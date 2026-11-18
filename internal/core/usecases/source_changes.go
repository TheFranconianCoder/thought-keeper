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

// materialChange says whether a refresh is worth telling the reader about. The snapshot is replaced
// whenever the text differs — the vault stays current — but the change time, the updated mark and the
// change record are reserved for a change a reader would notice: the title moved, or at least
// ChangeMinWords word positions differ and those words average more than ChangeMinWordLength
// characters. A ticking timestamp, a view counter, a typo or a reflow is not an event. Zero words
// turns the rule off: any difference in the body is material (the pre-threshold behavior).
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
	if shift.Words < uc.ChangeMinWords || shift.AvgRunes <= float64(uc.ChangeMinWordLength) {
		return false, shift
	}
	return true, shift
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
