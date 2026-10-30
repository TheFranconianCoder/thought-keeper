package agents

import "strings"

// languageNames maps the codes the app is likely to meet to the English name a prompt can use. A
// code outside this table is passed through as-is: an endpoint that understands "pt-BR" is more
// useful than one that is silently called "English". The mapping is deliberately small — it is a
// prompt hint, not a locale database.
var languageNames = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish", "it": "Italian",
	"nl": "Dutch", "pt": "Portuguese", "pl": "Polish", "ru": "Russian", "uk": "Ukrainian",
	"cs": "Czech", "sk": "Slovak", "sv": "Swedish", "da": "Danish", "nb": "Norwegian",
	"fi": "Finnish", "tr": "Turkish", "el": "Greek", "hu": "Hungarian", "ro": "Romanian",
	"ja": "Japanese", "ko": "Korean", "zh": "Chinese", "ar": "Arabic", "he": "Hebrew",
	"hi": "Hindi", "id": "Indonesian", "vi": "Vietnamese", "th": "Thai",
	"eng": "English", "deu": "German", "fra": "French", "spa": "Spanish", "ita": "Italian",
}

// LanguageName returns the English name a prompt uses for a language code. An empty code is English:
// that is the default of every content-language setting.
func LanguageName(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return "English"
	}
	if name, ok := languageNames[code]; ok {
		return name
	}
	return code
}

// distillSystemFor returns the distill system prompt written in the configured language. The English
// prompt is the reference text; the other languages only replace the two clauses that name it, so
// the rules stay one text and cannot drift apart.
func distillSystemFor(code string) string {
	name := strings.ToUpper(LanguageName(code))
	if name == "ENGLISH" {
		return DistillSystem
	}
	return strings.NewReplacer(
		"in ENGLISH, regardless of the source's language", "in "+name+", regardless of the source's language",
		"short, flat, ENGLISH topic", "short, flat, "+name+" topic",
	).Replace(DistillSystem)
}

// summarySystemFor returns the summary system prompt written in the configured language.
func summarySystemFor(code string) string {
	name := strings.ToUpper(LanguageName(code))
	if name == "ENGLISH" {
		return SummarySystem
	}
	return strings.ReplaceAll(SummarySystem,
		"Write in ENGLISH, regardless of the source's language.",
		"Write in "+name+", regardless of the source's language.")
}

// noClaimsSummaries are the honest one-line overviews for a source whose derivation produced no
// claim (a cover, a link list). It is written to the vault, so it follows the summary language.
var noClaimsSummaries = map[string]string{
	"en": "_This source produced no claims, so there is nothing to summarise._",
	"de": "_Diese Quelle hat keine Aussagen ergeben, es gibt daher nichts zusammenzufassen._",
}

// NoClaimsSummary returns the one-line overview for a source that produced no claims.
func NoClaimsSummary(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if line, ok := noClaimsSummaries[code]; ok {
		return line
	}
	return noClaimsSummaries["en"]
}

// DistillSystemFor is the exported form of the configured distill prompt.
func DistillSystemFor(code string) string { return distillSystemFor(code) }

// SummarySystemFor is the exported form of the configured summary prompt.
func SummarySystemFor(code string) string { return summarySystemFor(code) }
