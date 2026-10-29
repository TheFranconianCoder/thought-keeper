// Package usecases implements the application's use cases over the vault, the index and the agent.
package usecases

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSince turns a question's time window into an absolute instant. It accepts an RFC3339
// timestamp or a duration back from now ("24h", "168h", "7d", "1w"). An empty value means "no
// window" and returns the zero time.
//
// The window is deliberately relative to the CALLER's clock: the agent asks for "the last 24
// hours", and the use case decides what that means at the moment the question is answered.
func ParseSince(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if d, ok := parseWindowDuration(raw); ok {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("since %q must be a positive window", raw)
		}
		return now.Add(-d).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf(
		"invalid since %q: use an RFC3339 timestamp or a duration such as 24h or 7d", raw)
}

// parseWindowDuration reads the shapes a model actually writes. time.ParseDuration covers "24h"
// and "168h"; the day/week suffixes are added because "7d" is shorter and reads more naturally
// than "168h".
func parseWindowDuration(raw string) (time.Duration, bool) {
	if len(raw) > 1 {
		suffix := raw[len(raw)-1]
		if suffix == 'd' || suffix == 'w' {
			n, err := strconv.Atoi(raw[:len(raw)-1])
			if err == nil {
				unit := 24 * time.Hour
				if suffix == 'w' {
					unit = 7 * 24 * time.Hour
				}
				return time.Duration(n) * unit, true
			}
		}
	}
	d, err := time.ParseDuration(raw)
	return d, err == nil
}
