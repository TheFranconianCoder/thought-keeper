package markdown_test

import (
	"reflect"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
)

func TestTags(t *testing.T) {
	body := "# Heading\nA #caching and #area/health tag.\n`#code`\n```\n#fenced\n```\n"
	got := markdown.Tags(body)
	want := []string{"caching", "area/health"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}
