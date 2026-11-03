// Package dates reads a publication date out of what a source declares about itself: a page's
// metadata, a <time> element, a feed item, or the line of prose under a title.
//
// The rule is deliberately narrow. A date needs a day, a month and a year; an ambiguous numeric
// form is read the European way (day first) and anything that cannot be a real date is refused. A
// wrong date is worse than none: the reader sees it beside the arrival and has no way to tell.
package dates

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// minYear and maxAhead bound a plausible publication: an article from before 1900 is not what a
// vault of sources carries, and a date in the far future is a template placeholder.
const (
	minYear  = 1900
	maxAhead = 365 * 24 * time.Hour
)

// exact are the formats whose whole value is one date. The zone is kept when the value carries one.
var exact = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z0700",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z0700",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"02.01.2006",
	"2.1.2006",
	"02. January 2006",
	"2. January 2006",
	"January 2, 2006",
	"Jan 2, 2006",
}

var (
	// isoDate is an ISO date anywhere in a longer value.
	isoDate = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	// dotted is the European numeric form: day.month.year.
	dotted = regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\.(\d{4})\b`)
	// dayMonth is "27. Oktober 2026" / "27 Oct 2026".
	dayMonth = regexp.MustCompile(`\b(\d{1,2})\.?\s+([\p{L}]{3,})\.?\s+(\d{4})\b`)
	// monthDay is "October 27, 2026" / "Oct 27 2026".
	monthDay = regexp.MustCompile(`\b([\p{L}]{3,})\.?\s+(\d{1,2}),?\s+(\d{4})\b`)
	// label marks a publication date in prose. "updated" does not match: the word boundary sits
	// between the prefix and "date".
	label = regexp.MustCompile(`(?i)\b(published|posted|veröffentlicht|erschienen|publication)\b`)
)

// months maps the month names this package understands to their number. English and German: the
// interface and the content the vault is written in.
var months = map[string]time.Month{
	"jan": time.January, "january": time.January, "januar": time.January,
	"feb": time.February, "february": time.February, "februar": time.February,
	"mar": time.March, "march": time.March, "mär": time.March, "mrz": time.March,
	"märz": time.March, "maerz": time.March,
	"apr": time.April, "april": time.April,
	"may": time.May, "mai": time.May,
	"jun": time.June, "june": time.June, "juni": time.June,
	"jul": time.July, "july": time.July, "juli": time.July,
	"aug": time.August, "august": time.August,
	"sep": time.September, "sept": time.September, "september": time.September,
	"oct": time.October, "october": time.October, "okt": time.October, "oktober": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December, "dez": time.December, "dezember": time.December,
}

// Parse reads the first full date in s. A value that IS a date keeps its time and zone; a date inside
// a longer value is taken at midnight UTC.
func Parse(s string) (time.Time, bool) {
	text := strings.TrimSpace(s)
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range exact {
		if t, err := time.Parse(layout, text); err == nil {
			return t, true
		}
	}
	found := findAll(text)
	if len(found) == 0 {
		return time.Time{}, false
	}
	return found[0].at, true
}

// FromText finds a date in prose. A date next to a publication label wins; otherwise only a date in
// the HEAD of the text counts (the byline under a title), which is what keeps a date mentioned deep
// in an article from being mistaken for its publication.
func FromText(text string, now time.Time) (time.Time, bool) {
	const head = 200
	found := findAll(text)
	if len(found) == 0 {
		return time.Time{}, false
	}
	for _, hit := range found {
		if labelled(text, hit.start) && plausible(hit.at, now) {
			return hit.at, true
		}
	}
	if found[0].start <= head && plausible(found[0].at, now) {
		return found[0].at, true
	}
	return time.Time{}, false
}

// dateHit is one date found in a text and where it starts.
type dateHit struct {
	at    time.Time
	start int
}

// findAll collects every full date in text, in order of appearance.
func findAll(text string) []dateHit {
	var out []dateHit
	for _, loc := range isoDate.FindAllStringSubmatchIndex(text, -1) {
		year, mon, day := number(text, loc, 1), number(text, loc, 2), number(text, loc, 3)
		if t, ok := makeDate(year, mon, day); ok {
			out = append(out, dateHit{at: t, start: loc[0]})
		}
	}
	for _, loc := range dotted.FindAllStringSubmatchIndex(text, -1) {
		day, mon, year := number(text, loc, 1), number(text, loc, 2), number(text, loc, 3)
		if t, ok := makeDate(year, mon, day); ok {
			out = append(out, dateHit{at: t, start: loc[0]})
		}
	}
	for _, loc := range dayMonth.FindAllStringSubmatchIndex(text, -1) {
		day, mon, year := number(text, loc, 1), month(text, loc, 2), number(text, loc, 3)
		if t, ok := makeDate(year, mon, day); ok {
			out = append(out, dateHit{at: t, start: loc[0]})
		}
	}
	for _, loc := range monthDay.FindAllStringSubmatchIndex(text, -1) {
		mon, day, year := month(text, loc, 1), number(text, loc, 2), number(text, loc, 3)
		if t, ok := makeDate(year, mon, day); ok {
			out = append(out, dateHit{at: t, start: loc[0]})
		}
	}
	slices.SortFunc(out, func(a, b dateHit) int { return a.start - b.start })
	return out
}

// labelled reports whether a publication word sits shortly before the date.
func labelled(text string, at int) bool {
	from := max(0, at-60)
	return label.MatchString(text[from:at])
}

// makeDate builds a UTC date, refusing a value that cannot be one.
func makeDate(year, month, day int) (time.Time, bool) {
	if year < minYear || year > 2200 || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	// time.Date normalises an impossible day (Feb 31 → Mar 3); a date that moved is not a date.
	if t.Day() != day || int(t.Month()) != month {
		return time.Time{}, false
	}
	return t, true
}

// plausible bounds a date to what a source's own date can be.
func plausible(t time.Time, now time.Time) bool {
	if t.IsZero() || t.Year() < minYear {
		return false
	}
	return !t.After(now.Add(maxAhead))
}

func number(text string, loc []int, group int) int {
	if group*2+1 >= len(loc) || loc[group*2] < 0 {
		return 0
	}
	n, err := strconv.Atoi(text[loc[group*2]:loc[group*2+1]])
	if err != nil {
		return 0
	}
	return n
}

func month(text string, loc []int, group int) int {
	if group*2+1 >= len(loc) || loc[group*2] < 0 {
		return 0
	}
	name := strings.ToLower(strings.TrimSuffix(text[loc[group*2]:loc[group*2+1]], "."))
	if m, ok := months[name]; ok {
		return int(m)
	}
	return 0
}
