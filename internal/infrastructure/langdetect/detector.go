package langdetect

import "strings"

// Detector is the app's language detector. It is a thin adapter over the embedded trigram/script
// detector (MIT-licensed, see LICENSE): 84 languages, no network, no CGO, no model call.
type Detector struct{}

// The embedded detector compares the positions of the sample's most frequent trigrams with a
// language profile. Its score assumes a SHORT sample: once a document has thousands of distinct
// trigrams, the positions dwarf the profile and the confidence turns negative. A long book therefore
// looked unclassifiable. The adapter samples windows of a safe size instead of handing it the whole
// document.
const (
	detectWindowRunes = 1000
	detectWindows     = 8
)

// Detect returns an ISO 639-1 code (or ISO 639-3 when no 639-1 exists), or "" for text that cannot
// be classified. The value is a HINT for search and the source record, never a hard filter: a vault
// is allowed to contain code, names and mixed-language prose.
//
// Long texts are read in up to eight windows spread over the document. A code-heavy opening thus
// cannot decide the language of the whole source, and a book is classified from its prose rather
// than from the size of its vocabulary.
func (Detector) Detect(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) < 8 {
		return ""
	}
	if len(runes) <= detectWindowRunes {
		return languageCode(Detect(text))
	}

	step := max((len(runes)-detectWindowRunes)/(detectWindows-1), 1)
	type vote struct {
		count      int
		confidence float64
	}
	votes := make(map[string]vote, detectWindows)
	for window := range detectWindows {
		start := window * step
		if start+detectWindowRunes > len(runes) {
			start = len(runes) - detectWindowRunes
		}
		info := Detect(string(runes[start : start+detectWindowRunes]))
		code := languageCode(info)
		if code == "" {
			continue
		}
		v := votes[code]
		v.count++
		v.confidence += info.Confidence
		votes[code] = v
	}

	best := ""
	var bestVote vote
	for code, v := range votes {
		if v.count > bestVote.count ||
			(v.count == bestVote.count && v.confidence > bestVote.confidence) ||
			(v.count == bestVote.count && v.confidence == bestVote.confidence && code < best) {
			best, bestVote = code, v
		}
	}
	return best
}

func languageCode(info Info) string {
	if info.Lang < 0 || info.Confidence <= 0 {
		return ""
	}
	if code := info.Lang.Iso6391(); code != "" {
		return code
	}
	return info.Lang.Iso6393()
}
