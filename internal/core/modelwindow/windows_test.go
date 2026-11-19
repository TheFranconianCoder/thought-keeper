package modelwindow

import (
	"testing"
	"time"
)

// at builds a UTC instant on one fixed day, so the tests read as clock times.
func at(hour, minute int) time.Time {
	return time.Date(2026, time.November, 19, hour, minute, 0, 0, time.UTC)
}

func mustParse(t *testing.T, values ...string) Windows {
	t.Helper()
	w, err := Parse(values)
	if err != nil {
		t.Fatalf("Parse(%v): %v", values, err)
	}
	return w
}

func TestParseNormalizesAndReports(t *testing.T) {
	w := mustParse(t, " 01:00-04:00 ", "6:00-10:00")
	if got := w.Strings(); len(got) != 2 || got[0] != "01:00-04:00" || got[1] != "06:00-10:00" {
		t.Fatalf("Strings() = %v", got)
	}
	if w.Empty() {
		t.Fatal("a parsed set must not be empty")
	}
	if !(Windows{}).Empty() {
		t.Fatal("the zero value must be empty")
	}
}

func TestParseRefusesBrokenValues(t *testing.T) {
	for _, value := range []string{
		"",
		"01:00",
		"24:00-04:00",
		"01:60-04:00",
		"aa:00-04:00",
		"01:00-01:00",
		"01:00-04:00-06:00",
	} {
		if _, err := Parse([]string{value}); err == nil {
			t.Errorf("Parse(%q) accepted a value it must refuse", value)
		}
	}
}

func TestClosedAtNormalRange(t *testing.T) {
	w := mustParse(t, "01:00-04:00")
	cases := []struct {
		when   time.Time
		closed bool
		reopen time.Time
	}{
		{at(0, 59), false, time.Time{}},
		{at(1, 0), true, at(4, 0)}, // start is inclusive
		{at(2, 30), true, at(4, 0)},
		{at(3, 59), true, at(4, 0)},
		{at(4, 0), false, time.Time{}}, // end is exclusive
		{at(12, 0), false, time.Time{}},
	}
	for _, c := range cases {
		closed, reopen := w.ClosedAt(c.when)
		if closed != c.closed {
			t.Errorf("%s: closed = %v, want %v", c.when.Format("15:04"), closed, c.closed)
			continue
		}
		if !reopen.Equal(c.reopen) {
			t.Errorf("%s: reopen = %s, want %s", c.when.Format("15:04"), reopen, c.reopen)
		}
	}
}

func TestClosedAtWrappingRange(t *testing.T) {
	w := mustParse(t, "23:00-02:00")
	cases := []struct {
		when   time.Time
		closed bool
		reopen time.Time
	}{
		{at(22, 59), false, time.Time{}},
		{at(23, 0), true, at(2, 0).Add(24 * time.Hour)},
		{at(23, 30), true, at(2, 0).Add(24 * time.Hour)},
		{at(0, 30), true, at(2, 0)},
		{at(1, 59), true, at(2, 0)},
		{at(2, 0), false, time.Time{}},
		{at(12, 0), false, time.Time{}},
	}
	for _, c := range cases {
		closed, reopen := w.ClosedAt(c.when)
		if closed != c.closed {
			t.Errorf("%s: closed = %v, want %v", c.when.Format("15:04"), closed, c.closed)
			continue
		}
		if !reopen.Equal(c.reopen) {
			t.Errorf("%s: reopen = %s, want %s", c.when.Format("15:04"), reopen, c.reopen)
		}
	}
}

func TestClosedAtMergesOverlappingWindows(t *testing.T) {
	w := mustParse(t, "01:00-04:00", "03:00-05:00")
	closed, reopen := w.ClosedAt(at(3, 0))
	if !closed {
		t.Fatal("03:00 is inside both windows")
	}
	if !reopen.Equal(at(5, 0)) {
		t.Fatalf("reopen = %s, want 05:00 (the union ends with the later window)", reopen)
	}
}

func TestClosedAtUsesUTCRegardlessOfTheLocation(t *testing.T) {
	w := mustParse(t, "01:00-04:00")
	berlin := time.FixedZone("CET", 2*60*60)
	// 02:30 in Berlin is 00:30 UTC: still open.
	if closed, _ := w.ClosedAt(time.Date(2026, time.November, 19, 2, 30, 0, 0, berlin)); closed {
		t.Fatal("the window must be read on the UTC clock, not the caller's zone")
	}
}

func TestNextClose(t *testing.T) {
	w := mustParse(t, "01:00-04:00", "06:00-10:00")
	cases := []struct {
		when time.Time
		want time.Time
	}{
		{at(0, 30), at(1, 0)},
		{at(1, 0), at(6, 0)}, // already inside the first: the NEXT start is the second
		{at(5, 0), at(6, 0)},
		{at(11, 0), at(1, 0).Add(24 * time.Hour)},
		{at(23, 59), at(1, 0).Add(24 * time.Hour)},
	}
	for _, c := range cases {
		next, ok := w.NextClose(c.when)
		if !ok {
			t.Fatalf("%s: no next close", c.when.Format("15:04"))
		}
		if !next.Equal(c.want) {
			t.Errorf("%s: next close = %s, want %s", c.when.Format("15:04"), next, c.want)
		}
	}
	if _, ok := (Windows{}).NextClose(at(12, 0)); ok {
		t.Error("an empty set has no next close")
	}
}
