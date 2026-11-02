package usecases

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// resolveCommandWindow fills From/To/Timezone on the reading. The spec stays the truth for "run
// again"; the instants are the audit of this run.
func resolveCommandWindow(criteria *entities.CommandCriteria, now time.Time, timezone string) error {
	loc := time.Local
	if timezone != "" {
		if loaded, err := time.LoadLocation(timezone); err == nil {
			loc = loaded
		}
	}
	from, to, err := ResolveCommandWindow(criteria.Window.Spec, now, loc)
	if err != nil {
		return err
	}
	criteria.Window.From = from
	criteria.Window.To = to
	criteria.Window.Timezone = loc.String()
	return nil
}

// ResolveCommandWindow turns a command's window SPEC into absolute instants in the reader's
// timezone. The spec is kept with the record, so "run again" re-anchors "yesterday" to the new
// yesterday instead of freezing a pair of instants; the instants are kept for the audit.
//
// Calendar terms use the reader's calendar (Monday weeks, local midnight); "last-Nd/w/m" are
// rolling windows back from now. The end is always exclusive. The vault only stores ARRIVAL for a
// source, CREATION for a claim and the last EDIT for a wiki page — never publication.
func ResolveCommandWindow(spec string, now time.Time, loc *time.Location) (from, to time.Time, err error) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return time.Time{}, time.Time{}, nil
	}
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)

	switch spec {
	case "today":
		return startOfDay(now).UTC(), now.UTC(), nil
	case "yesterday":
		today := startOfDay(now)
		return today.AddDate(0, 0, -1).UTC(), today.UTC(), nil
	case "since-yesterday":
		return startOfDay(now).AddDate(0, 0, -1).UTC(), now.UTC(), nil
	case "this-week":
		return startOfWeek(now).UTC(), now.UTC(), nil
	case "last-week":
		thisWeek := startOfWeek(now)
		return thisWeek.AddDate(0, 0, -7).UTC(), thisWeek.UTC(), nil
	case "this-month":
		return startOfMonth(now).UTC(), now.UTC(), nil
	case "last-month":
		thisMonth := startOfMonth(now)
		return thisMonth.AddDate(0, -1, 0).UTC(), thisMonth.UTC(), nil
	}

	if after, ok := strings.CutPrefix(spec, "last-"); ok {
		raw := after
		if len(raw) < 2 {
			return time.Time{}, time.Time{}, fmt.Errorf("unknown time window %q", spec)
		}
		unit := raw[len(raw)-1]
		n, parseErr := strconv.Atoi(raw[:len(raw)-1])
		if parseErr != nil || n <= 0 {
			return time.Time{}, time.Time{}, fmt.Errorf("unknown time window %q", spec)
		}
		switch unit {
		case 'h':
			return now.Add(-time.Duration(n) * time.Hour).UTC(), now.UTC(), nil
		case 'd':
			return now.AddDate(0, 0, -n).UTC(), now.UTC(), nil
		case 'w':
			return now.AddDate(0, 0, -7*n).UTC(), now.UTC(), nil
		case 'm':
			return now.AddDate(0, -n, 0).UTC(), now.UTC(), nil
		}
		return time.Time{}, time.Time{}, fmt.Errorf("unknown time window %q", spec)
	}

	// An absolute range: "2026-10-01..2026-10-31" (both days inclusive) or a single day.
	if strings.Contains(spec, "..") {
		parts := strings.SplitN(spec, "..", 2)
		start, startErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(parts[0]), loc)
		end, endErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(parts[1]), loc)
		if startErr != nil || endErr != nil || end.Before(start) {
			return time.Time{}, time.Time{}, fmt.Errorf("unknown time window %q", spec)
		}
		return startOfDay(start).UTC(), startOfDay(end).AddDate(0, 0, 1).UTC(), nil
	}
	if day, dayErr := time.ParseInLocation("2006-01-02", spec, loc); dayErr == nil {
		return startOfDay(day).UTC(), startOfDay(day).AddDate(0, 0, 1).UTC(), nil
	}

	return time.Time{}, time.Time{}, fmt.Errorf("unknown time window %q", spec)
}

// WindowContains reports whether t falls in [from, to). A zero from or to is an open bound; a zero
// window (both bounds zero) contains everything.
func WindowContains(t, from, to time.Time) bool {
	if !from.IsZero() && t.Before(from) {
		return false
	}
	if !to.IsZero() && !t.Before(to) {
		return false
	}
	return true
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// startOfWeek is Monday: the vault's readers write German and English, and ISO weeks are the
// calendar both languages mean by "this week".
func startOfWeek(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return startOfDay(t).AddDate(0, 0, -(weekday - 1))
}

func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}
