package markdown

import (
	"strings"
	"unicode"
)

// translit maps Latin letters with diacritics to plain ASCII. German umlauts use
// the conventional ae/oe/ue spelling (and ß → ss); other marks are dropped. Tags
// and slugs are therefore always pure ASCII.
var translit = map[rune]string{
	'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss",
	'á': "a", 'à': "a", 'â': "a", 'ã': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'ç': "c", 'ć': "c", 'č': "c", 'ĉ': "c", 'ċ': "c",
	'ď': "d", 'đ': "d",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ğ': "g", 'ĝ': "g", 'ġ': "g", 'ģ': "g",
	'ĥ': "h", 'ħ': "h",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i", 'ı': "i",
	'ĵ': "j",
	'ķ': "k",
	'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ł': "l",
	'ñ': "n", 'ń': "n", 'ņ': "n", 'ň': "n",
	'ó': "o", 'ò': "o", 'ô': "o", 'õ': "o", 'ø': "o", 'ō': "o", 'ŏ': "o", 'ő': "o",
	'ŕ': "r", 'ř': "r",
	'ś': "s", 'š': "s", 'ş': "s", 'ș': "s",
	'ť': "t", 'ţ': "t", 'ț': "t",
	'ú': "u", 'ù': "u", 'û': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u",
	'ý': "y", 'ÿ': "y",
	'ź': "z", 'ż': "z", 'ž': "z",
	'æ': "ae", 'œ': "oe", 'þ': "th",
}

// Slug derives a stable, URL-safe slug from a title or claim. It lowercases and
// transliterates to ASCII, turns any run of non-alphanumerics into a single hyphen,
// and trims.
func Slug(s string) string {
	var b strings.Builder
	lastHyphen := true // trims leading hyphens
	for _, r := range strings.ToLower(s) {
		ascii := asciiOf(r)
		switch ascii {
		case "":
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		default:
			b.WriteString(ascii)
			lastHyphen = false
		}
	}
	out := strings.Trim(b.String(), "-")
	const max = 60
	if len(out) > max {
		out = strings.Trim(out[:max], "-")
	}
	if out == "" {
		out = "untitled"
	}
	return out
}

// asciiOf returns the ASCII form of r, or "" when it is not alphanumeric ASCII.
func asciiOf(r rune) string {
	if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
		return string(r)
	}
	if r >= 'A' && r <= 'Z' {
		return string(r + 32)
	}
	if r < 128 {
		return ""
	}
	if repl, ok := translit[unicode.ToLower(r)]; ok {
		return repl
	}
	return ""
}

// SlugPath slugifies a slash-separated path, keeping the hierarchy: "Software
// Engineering/Caching" → "software-engineering/caching".
func SlugPath(s string) string {
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, Slug(p))
	}
	if len(out) == 0 {
		return "untitled"
	}
	return strings.Join(out, "/")
}

// NormalizeTag turns a free-form tag into a stable ASCII tag: lowercase,
// transliterated, and limited to letters, digits, '-', '_' and '/'.
func NormalizeTag(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case '/', '_', '-':
			b.WriteRune(r)
			lastDash = false
		default:
			ascii := asciiOf(r)
			if ascii == "" {
				if !lastDash && b.Len() > 0 {
					b.WriteByte('-')
					lastDash = true
				}
				continue
			}
			b.WriteString(ascii)
			lastDash = false
		}
	}
	return strings.Trim(b.String(), "-/_")
}

// NormalizeBranch turns a free-form branch name into a folder-safe slug.
func NormalizeBranch(s string) string {
	return Slug(s)
}

// NormalizeTopic is the flat claim-topic normaliser: a lowercase ASCII slug.
func NormalizeTopic(s string) string { return Slug(s) }
