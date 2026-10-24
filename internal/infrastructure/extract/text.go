package extract

import (
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// textSections splits plain text on blank lines into flat paragraph parts.
func textSections(data []byte) ([]entities.Part, error) {
	var parts []entities.Part
	for _, block := range splitParagraphs(string(data)) {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		parts = append(parts, entities.Part{Level: 1, Title: firstLine(block), Text: block})
	}
	return parts, nil
}

func splitParagraphs(s string) []string {
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	const max = 80
	if len(s) > max {
		return strings.TrimSpace(s[:max])
	}
	return s
}
