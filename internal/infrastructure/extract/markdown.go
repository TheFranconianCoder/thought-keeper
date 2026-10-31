package extract

import (
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// markdownSections splits Markdown by ATX headings into nested parts. A YAML
// frontmatter block is dropped; fenced code is never treated as headings.
func markdownSections(data []byte) ([]entities.Part, error) {
	return splitMarkdown(stripFrontmatter(string(data))), nil
}

// splitMarkdown splits plain Markdown (no frontmatter to strip) into nested parts, one per heading.
func splitMarkdown(md string) []entities.Part {
	lines := strings.Split(md, "\n")

	var parts []entities.Part
	cur := entities.Part{Level: 0}
	inFence := false
	// The body accumulates in a builder, not in `cur.Text += line`: a document is one long string
	// and re-copying it per LINE is quadratic — the old shape of this parser paid the whole text
	// again for every line it read.
	var body strings.Builder

	push := func() {
		text := strings.TrimSpace(body.String())
		if text == "" && cur.Title == "" {
			return
		}
		cur.Text = text
		parts = append(parts, cur)
		body.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			body.WriteString(line)
			body.WriteByte('\n')
			continue
		}
		if !inFence {
			if level, title, ok := heading(line); ok {
				push()
				cur = entities.Part{Level: level, Title: title}
				continue
			}
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	push()
	return parts
}

// heading returns the level and text of an ATX heading line ("## Title").
func heading(line string) (int, string, bool) {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 {
		return 0, "", false
	}
	if i >= len(line) || line[i] != ' ' {
		return 0, "", false
	}
	return i, strings.TrimSpace(line[i+1:]), true
}

// stripFrontmatter removes a leading YAML frontmatter block.
func stripFrontmatter(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return s
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return s
}
