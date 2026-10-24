package interfaces

// LanguageDetector recognises the language of source text. It returns an ISO 639-1 code where one
// exists (otherwise ISO 639-3), or "" when the sample is too short or too ambiguous. It is a hint
// for search and the source record, never a filter: mixed-language and code-heavy sources exist.
type LanguageDetector interface {
	Detect(text string) string
}
