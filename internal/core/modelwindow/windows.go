// Package modelwindow is the schedule of the derivation's model calls: the UTC ranges in which the
// app must not call the model (its provider's expensive hours), and when the next one opens.
//
// A window is a pair of wall-clock times in UTC, start inclusive and end exclusive
// ("01:00-04:00"). A range whose start is later than its end wraps midnight ("23:00-02:00"). The
// package is pure: no clock, no config, no model — the caller passes the instant it asks about.
package modelwindow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// window is one blocked range, in minutes of the UTC day.
type window struct {
	start int
	end   int
}

// Windows is the ordered set of blocked ranges. The zero value is empty and blocks nothing.
type Windows struct {
	windows []window
}

// Parse reads the configured windows. Every value must be "HH:MM-HH:MM" in UTC; the error names the
// value that is not, so the Config overlay can say what to fix.
func Parse(values []string) (Windows, error) {
	out := Windows{windows: make([]window, 0, len(values))}
	for _, raw := range values {
		win, err := parseOne(raw)
		if err != nil {
			return Windows{}, err
		}
		out.windows = append(out.windows, win)
	}
	return out, nil
}

// parseOne reads one "HH:MM-HH:MM" range. The endpoints are two clock times, so both sides take the
// same form; a range that starts and ends at the same minute is refused (it would mean everything or
// nothing, and neither is what the reader wrote).
func parseOne(raw string) (window, error) {
	value := strings.TrimSpace(raw)
	start, end, ok := strings.Cut(value, "-")
	if !ok {
		return window{}, fmt.Errorf("model window %q: want HH:MM-HH:MM (UTC)", raw)
	}
	startMin, err := parseClock(start)
	if err != nil {
		return window{}, fmt.Errorf("model window %q: %w", raw, err)
	}
	endMin, err := parseClock(end)
	if err != nil {
		return window{}, fmt.Errorf("model window %q: %w", raw, err)
	}
	if startMin == endMin {
		return window{}, fmt.Errorf("model window %q: start and end are the same instant", raw)
	}
	return window{start: startMin, end: endMin}, nil
}

// parseClock reads one "HH:MM" time as minutes of the day.
func parseClock(value string) (int, error) {
	hour, minute, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return 0, fmt.Errorf("%q is not HH:MM", value)
	}
	h, err := strconv.Atoi(hour)
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("%q is not an hour of the day", value)
	}
	m, err := strconv.Atoi(minute)
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("%q is not a minute of the hour", value)
	}
	return h*60 + m, nil
}

// Empty reports whether no window is configured: the model may always run.
func (w Windows) Empty() bool { return len(w.windows) == 0 }

// Strings returns the windows in their configured order, as the app reports them.
func (w Windows) Strings() []string {
	out := make([]string, 0, len(w.windows))
	for _, win := range w.windows {
		out = append(out, fmt.Sprintf("%02d:%02d-%02d:%02d", win.start/60, win.start%60, win.end/60, win.end%60))
	}
	return out
}

// ClosedAt reports whether the derivation may not call the model at t, and when the block ends. The
// instant returned is the FIRST instant that is open again; it is the zero value when t is open.
// Overlapping windows are merged: the reopen is when the last of them ends.
func (w Windows) ClosedAt(t time.Time) (bool, time.Time) {
	closed := false
	end := time.Time{}
	for _, win := range w.windows {
		if isClosed, until := win.closedAt(t); isClosed {
			closed = true
			if until.After(end) {
				end = until
			}
		}
	}
	if !closed {
		return false, time.Time{}
	}
	// A second window may still be closed when the first ends; the union ends when the last one
	// ends. The loop is bounded by the number of windows, so a pathological set cannot spin.
	for round := 0; round < len(w.windows); round++ {
		extended := false
		for _, win := range w.windows {
			// end is exclusive: the last second inside the window is the one to test.
			if isClosed, until := win.closedAt(end.Add(-time.Second)); isClosed && until.After(end) {
				end, extended = until, true
			}
		}
		if !extended {
			break
		}
	}
	return true, end
}

// NextClose is the first instant at or after t in which a window begins, if any is configured. The
// gateway uses it to cut a call that would still run when the block starts.
func (w Windows) NextClose(t time.Time) (time.Time, bool) {
	var next time.Time
	for _, win := range w.windows {
		start := win.startInstant(t)
		if !start.After(t) {
			start = start.Add(24 * time.Hour)
		}
		if next.IsZero() || start.Before(next) {
			next = start
		}
	}
	return next, !next.IsZero()
}

// closedAt reports whether one window covers t, and when it ends. A wrapping window that began
// yesterday and still runs ends today; one that began today ends tomorrow.
func (win window) closedAt(t time.Time) (bool, time.Time) {
	t = t.UTC()
	midnight := midnightOf(t)
	minute := t.Hour()*60 + t.Minute()
	if win.start < win.end {
		if minute >= win.start && minute < win.end {
			return true, midnight.Add(time.Duration(win.end) * time.Minute)
		}
		return false, time.Time{}
	}
	// It wraps midnight.
	if minute >= win.start {
		return true, midnight.Add(24*time.Hour + time.Duration(win.end)*time.Minute)
	}
	if minute < win.end {
		return true, midnight.Add(time.Duration(win.end) * time.Minute)
	}
	return false, time.Time{}
}

// startInstant is the window's start on t's UTC day.
func (win window) startInstant(t time.Time) time.Time {
	return midnightOf(t.UTC()).Add(time.Duration(win.start) * time.Minute)
}

// midnightOf is 00:00 UTC of t's calendar day.
func midnightOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
