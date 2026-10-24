package usecases_test

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// TestSearchExcerptIsEnoughToAnswerFrom pins the point of the excerpt: the top search hits carry their
// text, because a hit you have to READ costs another round — and "search, read, search again, read
// again" is how a question that needs five rounds took sixteen.
func TestSearchExcerptIsEnoughToAnswerFrom(t *testing.T) {
	short := "A short page body."
	if got := usecases.Excerpt(short, 1200); got != short {
		t.Errorf("a body under the cap must come back unchanged, got %q", got)
	}

	long := strings.Repeat("word ", 1000)
	got := usecases.Excerpt(long, 100)
	if len(got) > 110 {
		t.Errorf("excerpt is %d characters, want about 100: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a cut excerpt must say that it was cut: %q", got)
	}
	if strings.HasSuffix(got, "wor…") {
		t.Errorf("the cut must land on a word boundary: %q", got)
	}
}
