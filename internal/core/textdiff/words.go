package textdiff

import (
	"unicode/utf8"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
)

// maxWordRunes bounds what counts as a word: a base64 data URI, a long identifier or a tracking
// token is not prose, and one of them must not decide that a page changed.
const maxWordRunes = 64

// WordShift is the size of a text change measured in WORDS — independent of line wrapping,
// punctuation and order. It is the measurement behind "is this page change worth telling the reader
// about?", where a line-based diff is too sensitive: a reflow or a typo would look like a change.
type WordShift struct {
	// Words is the number of word POSITIONS that differ: a replaced word counts once (not twice), an
	// added or a removed word counts once. A reflow, a punctuation fix or a moved paragraph leaves it
	// at zero.
	Words int
	// AvgRunes is the mean length, in characters, of the words on both sides of the difference
	// (textmatch.Tokens drops one-character noise, so "a" and "I" never decide anything).
	AvgRunes float64
}

// Same reports whether the two bodies say the same words.
func (w WordShift) Same() bool { return w.Words == 0 }

// Words compares two bodies word by word, using the vault's one tokenizer (lowercase, folded,
// letters and digits, one-character noise dropped). It counts the DIFFERENCE of the two word bags:
// words both sides carry do not count, wherever they sit.
func Words(oldText, newText string) WordShift {
	oldWords := textmatch.Tokens(oldText)
	newWords := textmatch.Tokens(newText)
	if len(oldWords) == 0 && len(newWords) == 0 {
		return WordShift{}
	}
	// The old body's bag. A word the new body still carries is consumed, not counted.
	remaining := make(map[string]int, len(oldWords))
	for _, word := range oldWords {
		if utf8.RuneCountInString(word) > maxWordRunes {
			continue
		}
		remaining[word]++
	}
	added, addedRunes := 0, 0
	for _, word := range newWords {
		length := utf8.RuneCountInString(word)
		if length > maxWordRunes {
			continue
		}
		if remaining[word] > 0 {
			remaining[word]--
			continue
		}
		added++
		addedRunes += length
	}
	removed, removedRunes := 0, 0
	for word, count := range remaining {
		removed += count
		removedRunes += count * utf8.RuneCountInString(word)
	}
	words := max(added, removed)
	if words == 0 {
		return WordShift{}
	}
	return WordShift{
		Words:    words,
		AvgRunes: float64(addedRunes+removedRunes) / float64(added+removed),
	}
}
