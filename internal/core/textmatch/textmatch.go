// Package textmatch is the pure lexical half of the app: how a word is folded, which inflection
// family it belongs to, and when two words count as the same term.
//
// Search and the interest matcher must agree on that answer — a reader who types "caching" into the
// search field and an interest that carries "cache" are asking the same question — so the rules live
// here and both sides import them. Nothing here touches the index or the network.
package textmatch

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Fold removes the accents a Latin word may carry, so a German title and an ASCII search term can
// meet: "Häuser" becomes "hauser", "café" becomes "cafe". It is deliberately a small table, not a
// Unicode normalisation: the vault's languages are the ones this list covers.
func Fold(s string) string {
	replacer := strings.NewReplacer(
		"ä", "a", "ö", "o", "ü", "u", "ß", "ss",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"á", "a", "à", "a", "â", "a", "ã", "a", "å", "a",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ø", "o",
		"ú", "u", "ù", "u", "û", "u", "ç", "c", "ñ", "n",
	)
	return replacer.Replace(s)
}

// languageSuffixes covers the languages that are common in a personal knowledge base. It is not a
// completeness claim: every entry is a hint, and a language absent here still gets the generic
// prefixes and commonSuffixes.
var languageSuffixes = map[string][]string{
	"de": {"isch", "lich", "keit", "heit", "ungen", "ung", "en", "er", "es", "em", "e", "s"},
	"en": {"ically", "ical", "ness", "ment", "ing", "ed", "ly", "ies", "es", "s"},
	"fr": {"iques", "ique", "ements", "ement", "ment", "es", "s", "e"},
	"es": {"ciones", "ción", "mente", "ados", "adas", "es", "os", "as", "s"},
	"it": {"mente", "zioni", "zione", "i", "e", "o", "a"},
	"pt": {"mente", "ções", "ção", "es", "os", "as", "s"},
	"nl": {"heid", "lijk", "en", "er", "es", "s"},
	"sv": {"het", "lig", "ing", "ar", "er", "en", "s"},
	"da": {"hed", "lig", "ing", "er", "en", "s"},
	"no": {"het", "lig", "ing", "er", "en", "s"},
	"ru": {"ами", "ями", "ого", "его", "ые", "ая", "ое", "ий", "ый", "а", "я", "ы", "и", "о", "е"},
	"uk": {"ами", "ями", "ого", "ому", "і", "и", "а", "я", "у", "ю", "е", "о"},
	"pl": {"ami", "ach", "ów", "y", "i", "a", "e", "u"},
	"cs": {"ami", "ách", "y", "i", "a", "e", "u"},
	"tr": {"leri", "ları", "lar", "ler", "i", "ı", "u", "ü", "e", "a"},
}

var commonSuffixes = []string{
	"ically", "ical", "tion", "sion", "ness", "ment", "isch", "lich", "keit", "heit",
	"ing", "ed", "ly", "es", "en", "er", "em", "ic", "al", "ous", "ive", "ique", "iques", "zione", "zioni", "mente", "s", "e",
}

// SuffixesFor returns the suffix list for the vault's language set. No languages means every known
// list: the matcher has no per-source language hint to lean on, and the broad list is the honest
// fallback. The fallback is cached; it is asked once per comparison otherwise.
func SuffixesFor(languages []string) []string {
	if len(languages) == 0 {
		return allSuffixes()
	}
	seen := map[string]bool{}
	out := make([]string, 0, 24)
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, language := range languages {
		for _, s := range languageSuffixes[language] {
			add(s)
		}
	}
	for _, s := range commonSuffixes {
		add(s)
	}
	return out
}

var (
	allOnce   sync.Once
	allCached []string
)

func allSuffixes() []string {
	allOnce.Do(func() {
		seen := map[string]bool{}
		out := make([]string, 0, 32)
		for _, list := range languageSuffixes {
			for _, s := range list {
				if s != "" && !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
		for _, s := range commonSuffixes {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		allCached = out
	})
	return allCached
}

// Variants turns one word into its search family: the word itself, the roots of common suffixes,
// and a short prefix family. The exact word is always first — a caller that only wants precision
// can stop there. A trailing "*" marks a prefix family.
func Variants(word string, languages []string) []string {
	seen := map[string]bool{}
	group := make([]string, 0, 8)
	add := func(term string) {
		term = strings.TrimSpace(strings.ToLower(term))
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		group = append(group, term)
	}
	add(word)
	lower := strings.ToLower(word)
	folded := Fold(lower)
	if folded != lower {
		add(folded)
	}
	for _, suffix := range SuffixesFor(languages) {
		for _, candidate := range []string{lower, folded} {
			if len(candidate) <= len(suffix)+2 || !strings.HasSuffix(candidate, suffix) {
				continue
			}
			root := strings.TrimSuffix(candidate, suffix)
			if len(root) >= 3 {
				add(root)
				add(root + "*")
			}
		}
	}
	addPrefixFamily(add, lower, folded)
	return group
}

// addPrefixFamily adds the short prefix that covers derivation across languages. It is only used
// for search RECALL; the interest matcher uses roots, not these prefixes (a three-letter prefix of
// "rust" would also catch "russia").
func addPrefixFamily(add func(string), lower, folded string) {
	runes := []rune(lower)
	if len(runes) == 0 {
		return
	}
	nonASCII := false
	for _, r := range runes {
		if r > unicode.MaxASCII {
			nonASCII = true
			break
		}
	}
	if len(runes) >= 5 {
		add(string(runes[:4]) + "*")
	} else if len(runes) >= 3 {
		add(string(runes[:3]) + "*")
	}
	if nonASCII {
		foldRunes := []rune(folded)
		if len(foldRunes) >= 4 {
			add(string(foldRunes[:3]) + "*")
		}
		if len(runes) >= 3 {
			add(string(runes[:3]) + "*")
		}
	}
	if nonLatinFirst(runes[0]) && len(runes) >= 2 {
		add(string(runes[:2]) + "*")
	}
}

func nonLatinFirst(r rune) bool {
	return (r >= 0x0400 && r <= 0x04FF) || (r >= 0x0370 && r <= 0x03FF) ||
		(r >= 0x0590 && r <= 0x05FF) || (r >= 0x0600 && r <= 0x06FF) ||
		(r >= 0x3040 && r <= 0x30FF) || (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

// Normalize is the form every comparison starts from: lowercase, accent-folded, trimmed.
func Normalize(s string) string { return Fold(strings.ToLower(strings.TrimSpace(s))) }

// Tokens splits text into the words a term may match: lowercase, folded, letters and digits only.
// Two-character tokens are kept on purpose (a German "KI" or an English "AI" is a word); one-letter
// noise is not.
func Tokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if utf8.RuneCountInString(f) < 2 {
			continue
		}
		out = append(out, Fold(f))
	}
	return out
}

// Term is one interest term, prepared for many comparisons. Its words are split once and their
// inflection roots are cached, because a matcher asks the same term about every claim of a source.
type Term struct {
	Raw   string
	Words []TermWord
}

// TermWord is one word of a term with its inflection roots.
type TermWord struct {
	Word  string
	Roots map[string]bool
}

// NewTerm prepares a term. It returns the zero value for an empty term.
func NewTerm(raw string) Term {
	raw = strings.TrimSpace(raw)
	words := termWords(raw)
	out := Term{Raw: raw, Words: make([]TermWord, 0, len(words))}
	for _, word := range words {
		out.Words = append(out.Words, TermWord{Word: word, Roots: roots(word)})
	}
	return out
}

// Empty reports a term that carries no comparable word.
func (t Term) Empty() bool { return len(t.Words) == 0 }

func roots(word string) map[string]bool {
	lower := Normalize(word)
	out := map[string]bool{lower: true}
	for _, suffix := range allSuffixes() {
		if len(lower) <= len(suffix)+2 || !strings.HasSuffix(lower, suffix) {
			continue
		}
		if root := strings.TrimSuffix(lower, suffix); root != "" {
			out[root] = true
		}
	}
	return out
}

// sharesRoot answers the strict word-family question: the same root, or a term of five or more
// characters that is a prefix of a longer word ("async" → "asynchronous"). A three-letter prefix is
// not enough for an interest — it would queue a source about Russia for a rule about Rust.
func sharesRoot(a TermWord, token string) bool {
	tk := Normalize(token)
	if tk == "" {
		return false
	}
	if tk == a.Word {
		return true
	}
	if utf8.RuneCountInString(a.Word) >= 5 && strings.HasPrefix(tk, a.Word) {
		return true
	}
	for root := range roots(tk) {
		if a.Roots[root] {
			return true
		}
	}
	return false
}

// InTokens reports whether every word of the term matches a token.
func (t Term) InTokens(tokens []string) bool {
	if t.Empty() {
		return false
	}
	for _, w := range t.Words {
		found := false
		for _, token := range tokens {
			if sharesRoot(w, token) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// InText is InTokens over raw text. A caller that compares many terms with the same text should
// tokenize once and use InTokens.
func (t Term) InText(text string) bool { return t.InTokens(Tokens(text)) }

// InTopic reports whether the term names a claim topic. Topics are flat slugs
// ("async-runtime"), so a term matches when it equals the whole slug or one of its parts.
func (t Term) InTopic(topic string) bool {
	parts := Tokens(topic)
	if len(parts) == 0 {
		return false
	}
	return t.InTokens(parts)
}

// WordsMatch is the single-pair convenience form of the same rule.
func WordsMatch(term, token string) bool {
	t := NewTerm(term)
	if t.Empty() {
		return false
	}
	return t.InTokens([]string{token})
}

func termWords(raw string) []string {
	fields := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if utf8.RuneCountInString(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

// Contains folds the haystack and the needle and answers a plain substring question. It is the
// fallback for terms with symbols (`c++`, `.net`) that tokenisation would destroy.
func Contains(haystack, needle string) bool {
	n := Normalize(needle)
	if n == "" {
		return false
	}
	return strings.Contains(Normalize(haystack), n)
}
