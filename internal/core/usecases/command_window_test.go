package usecases

import (
	"testing"
	"time"
)

func TestResolveCommandWindowCalendar(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("timezone not available: %v", err)
	}
	now := time.Date(2026, 11, 2, 19, 34, 0, 0, loc) // a Monday
	cases := []struct {
		spec string
		from time.Time
		to   time.Time
	}{
		{
			spec: "today",
			from: time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
			to:   now,
		},
		{
			spec: "yesterday",
			from: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
			to:   time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
		},
		{
			spec: "this-week",
			from: time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
			to:   now,
		},
		{
			spec: "last-week",
			from: time.Date(2026, 10, 26, 0, 0, 0, 0, loc),
			to:   time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
		},
		{
			spec: "this-month",
			from: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
			to:   now,
		},
		{
			spec: "last-month",
			from: time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
			to:   time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		},
		{
			spec: "2026-10-01..2026-10-31",
			from: time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
			to:   time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		},
		{
			spec: "2026-10-01",
			from: time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
			to:   time.Date(2026, 10, 2, 0, 0, 0, 0, loc),
		},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			from, to, err := ResolveCommandWindow(tc.spec, now, loc)
			if err != nil {
				t.Fatalf("ResolveCommandWindow: %v", err)
			}
			if !from.Equal(tc.from.UTC()) || !to.Equal(tc.to.UTC()) {
				t.Fatalf("%s = %v..%v, want %v..%v", tc.spec, from, to, tc.from.UTC(), tc.to.UTC())
			}
		})
	}
}

func TestResolveCommandWindowRolling(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 2, 19, 34, 0, 0, loc)
	cases := []struct {
		spec string
		from time.Time
	}{
		{"last-3h", now.Add(-3 * time.Hour)},
		{"last-2d", now.AddDate(0, 0, -2)},
		{"last-2w", now.AddDate(0, 0, -14)},
		{"last-2m", now.AddDate(0, -2, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			from, to, err := ResolveCommandWindow(tc.spec, now, loc)
			if err != nil {
				t.Fatalf("ResolveCommandWindow: %v", err)
			}
			if !from.Equal(tc.from.UTC()) || !to.Equal(now.UTC()) {
				t.Fatalf("%s = %v..%v, want %v..%v", tc.spec, from, to, tc.from.UTC(), now.UTC())
			}
		})
	}
}

func TestResolveCommandWindowDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("timezone not available: %v", err)
	}
	// The clock jumps forward on 2026-03-29 at 02:00. The calendar day still starts at local
	// midnight, which is +01:00, and yesterday ends there.
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, loc)
	from, to, err := ResolveCommandWindow("yesterday", now, loc)
	if err != nil {
		t.Fatalf("ResolveCommandWindow: %v", err)
	}
	wantFrom := time.Date(2026, 3, 28, 0, 0, 0, 0, loc)
	wantTo := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	if !from.Equal(wantFrom.UTC()) || !to.Equal(wantTo.UTC()) {
		t.Fatalf("yesterday = %v..%v, want %v..%v", from, to, wantFrom.UTC(), wantTo.UTC())
	}
}

func TestResolveCommandWindowRejectsUnknown(t *testing.T) {
	for _, spec := range []string{"whenever", "last-0d", "last-x", "2026-13-01"} {
		if _, _, err := ResolveCommandWindow(spec, time.Now(), time.UTC); err == nil {
			t.Fatalf("%q was accepted", spec)
		}
	}
}

func TestWindowContains(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if WindowContains(from.Add(-time.Second), from, to) {
		t.Fatal("a time before the window was contained")
	}
	if !WindowContains(from, from, to) {
		t.Fatal("the inclusive start was excluded")
	}
	if WindowContains(to, from, to) {
		t.Fatal("the exclusive end was included")
	}
	if !WindowContains(time.Now(), time.Time{}, time.Time{}) {
		t.Fatal("an empty window must contain everything")
	}
}
