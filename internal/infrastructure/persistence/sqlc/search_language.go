package sqlc

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Languages returns the language hints present in the vault. The SET, not a single language, is what
// the expansion uses: a vault may hold German and English sources, and a question may ask in either.
func (r *SearchRepository) Languages(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT meta FROM sources WHERE meta <> ''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m struct {
			Language string `json:"language"`
		}
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		lang := strings.ToLower(strings.TrimSpace(m.Language))
		if lang == "" || lang == "und" || seen[lang] {
			continue
		}
		seen[lang] = true
		out = append(out, lang)
	}
	sort.Strings(out)
	return out, rows.Err()
}

// variantGroups turns each query word into a group of terms: the word itself, the roots of common
// suffixes, and a short prefix family. Only the final FTS query combines the groups; exact search is
// always tried first, so these are a recall fallback, not a replacement.
//
// The prefix family is what covers derivation across languages: "Chaos" and "chaotisch" both reduce
// to "chao*"; language suffixes add the precise stems ("chaot*", "cach*", "map*") where they apply.
// The suffix table is deliberately broad; a language with no explicit entry still gets the generic
// prefixes and the common suffixes.
func variantGroups(words []string, languages []string) [][]string {
	if len(words) == 0 {
		return nil
	}
	out := make([][]string, 0, len(words))
	for _, word := range words {
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
		folded := foldWord(lower)
		if folded != lower {
			add(folded)
		}
		for _, suffix := range suffixesFor(languages) {
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
		addPrefixFamily(group, add, lower, folded)
		out = append(out, group)
	}
	return out
}

func suffixesFor(languages []string) []string {
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
	if len(languages) == 0 {
		for _, list := range languageSuffixes {
			for _, s := range list {
				add(s)
			}
		}
	}
	for _, s := range commonSuffixes {
		add(s)
	}
	return out
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

func addPrefixFamily(group []string, add func(string), lower, folded string) {
	_ = group
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
	// A Latin word of five or more characters can lose its last character without becoming a
	// different word family; for a short word, three characters are the useful floor.
	if len(runes) >= 5 {
		add(string(runes[:4]) + "*")
	} else if len(runes) >= 3 {
		add(string(runes[:3]) + "*")
	}
	if nonASCII {
		// "Häuser" and "Haus" share a meaning but not a byte prefix; a folded prefix and a slightly
		// shorter original prefix cover both sides of the umlaut.
		foldRunes := []rune(folded)
		if len(foldRunes) >= 4 {
			add(string(foldRunes[:3]) + "*")
		}
		if len(runes) >= 3 {
			add(string(runes[:3]) + "*")
		}
	}
	if utf8.RuneCountInString(lower) > 0 && nonLatinFirst(runes[0]) {
		// CJK and other scripts often have no whitespace-separated words; a two-rune prefix is the
		// smallest useful family. FTS still applies its own token boundaries.
		if len(runes) >= 2 {
			add(string(runes[:2]) + "*")
		}
	}
}

func nonLatinFirst(r rune) bool {
	return (r >= 0x0400 && r <= 0x04FF) || (r >= 0x0370 && r <= 0x03FF) ||
		(r >= 0x0590 && r <= 0x05FF) || (r >= 0x0600 && r <= 0x06FF) ||
		(r >= 0x3040 && r <= 0x30FF) || (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func foldWord(s string) string {
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

// ftsVariantQuery builds grouped FTS syntax: (word OR root* OR prefix*) AND (...). The terms are
// lowercased, quoted for FTS safety, and a trailing '*' becomes a prefix operator.
func ftsVariantQuery(groups [][]string, joiner string) string {
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		terms := make([]string, 0, len(group))
		for _, term := range group {
			prefix := strings.HasSuffix(term, "*")
			term = strings.TrimSuffix(term, "*")
			term = strings.ReplaceAll(term, `"`, `""`)
			if term == "" {
				continue
			}
			if prefix {
				terms = append(terms, `"`+term+`"*`)
			} else {
				terms = append(terms, `"`+term+`"`)
			}
		}
		if len(terms) > 0 {
			parts = append(parts, "("+strings.Join(terms, " OR ")+")")
		}
	}
	return strings.Join(parts, joiner)
}
