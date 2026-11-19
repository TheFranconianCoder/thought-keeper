package usecases

import (
	"strings"
	"testing"
)

// TestRequiredWords: the floor and the share, whichever asks for more. A long page must change more
// than a short one, and a short page is still governed by the floor.
func TestRequiredWords(t *testing.T) {
	for _, tc := range []struct {
		name    string
		words   int
		percent int
		base    int
		want    int
	}{
		{"floor only", 40, 0, 5000, 40},
		{"share below the floor", 40, 1, 1500, 40},
		{"share above the floor", 40, 1, 6000, 60},
		{"share rounds up", 40, 1, 6001, 61},
		{"an empty baseline cannot raise the floor", 40, 1, 0, 40},
		{"a short page is governed by the floor", 40, 100, 30, 40},
		{"the rule is off", 0, 10, 5000, 0},
	} {
		uc := URLs{ChangeMinWords: tc.words, ChangeMinPercent: tc.percent}
		if got := uc.requiredWords(tc.base); got != tc.want {
			t.Errorf("%s: requiredWords(%d) = %d, want %d", tc.name, tc.base, got, tc.want)
		}
	}
}

// TestMaterialChange: title, floor, share and prose length, in one rule. The measurement is taken
// against the OLD body, which is the stored snapshot.
func TestMaterialChange(t *testing.T) {
	long := strings.Repeat("database ", 6000)
	uc := URLs{ChangeMinWords: 40, ChangeMinPercent: 1, ChangeMinWordLength: 5}

	for _, tc := range []struct {
		name     string
		oldTitle string
		newTitle string
		oldBody  string
		newBody  string
		bodyDiff bool
		want     bool
	}{
		{
			name:     "a moved title is always an event",
			oldTitle: "Old", newTitle: "New", oldBody: long, newBody: long,
			want: true,
		},
		{
			name:     "an unchanged body is not",
			oldTitle: "Same", newTitle: "Same", oldBody: long, newBody: long,
			bodyDiff: false, want: false,
		},
		{
			name:     "one word is not",
			oldTitle: "Same", newTitle: "Same", oldBody: long, newBody: long + "database ",
			bodyDiff: true, want: false,
		},
		{
			name:     "the share is what a long page needs",
			oldTitle: "Same", newTitle: "Same", oldBody: long,
			newBody:  long + strings.Repeat("database ", 59),
			bodyDiff: true, want: false,
		},
		{
			name:     "the share is reached",
			oldTitle: "Same", newTitle: "Same", oldBody: long,
			newBody:  long + strings.Repeat("database ", 60),
			bodyDiff: true, want: true,
		},
		{
			name:     "short words do not make prose",
			oldTitle: "Same", newTitle: "Same", oldBody: strings.Repeat("dat ", 500),
			newBody:  strings.Repeat("dat ", 560),
			bodyDiff: true, want: false,
		},
	} {
		got, _ := uc.materialChange(tc.oldTitle, tc.newTitle, tc.oldBody, tc.newBody, tc.bodyDiff)
		if got != tc.want {
			t.Errorf("%s: material = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestMaterialChangeOff: zero words means any difference is an event, the pre-threshold behavior.
func TestMaterialChangeOff(t *testing.T) {
	uc := URLs{ChangeMinWords: 0, ChangeMinPercent: 10, ChangeMinWordLength: 5}
	if got, _ := uc.materialChange("Same", "Same", "one page", "another page", true); !got {
		t.Errorf("the rule is off but the change was refused")
	}
}
