package tree

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestIsNavigationTextRecognisesABreadcrumb(t *testing.T) {
	breadcrumb := "1. [Documentation](https://go.dev/doc/)\n2. [A Guide to the Go Garbage Collector](https://go.dev/doc/gc-guide)"
	if !IsNavigationText(breadcrumb) {
		t.Fatal("a link-only breadcrumb must be navigation")
	}
	if IsNavigationText("A link to [the docs](https://go.dev/doc/) explains the collector.") {
		t.Fatal("prose that contains a link is not navigation")
	}
	if IsNavigationText("[only one link](https://example.com/)") {
		t.Fatal("one link is not a menu")
	}
}

func TestBuildDropsABreadcrumbPart(t *testing.T) {
	nodes := Build([]entities.Part{
		{Title: "", Text: "1. [Documentation](https://go.dev/doc/)\n2. [A Guide](https://go.dev/doc/gc-guide)"},
		{Title: "Introduction", Level: 1, Text: "This guide explains the Go garbage collector."},
	})
	if len(nodes) != 2 { // root + Introduction
		t.Fatalf("nodes = %+v, want only the root and the Introduction", nodes)
	}
	if nodes[1].Title != "Introduction" || nodes[1].NodeID != "0.1" {
		t.Errorf("navigation leaked into the tree: %+v", nodes[1])
	}
}

func TestIsPageMatterDropsPDFCoverAndTableOfContents(t *testing.T) {
	cover := entities.Part{Page: 1, Title: "Page 1", Text: "Efficient Go Data-Driven Performance Optimization Bartłomiej Płotka"}
	if !IsPageMatter(cover) {
		t.Fatal("a short cover page without prose must be matter")
	}
	toc := entities.Part{Page: 6, Title: "Page 6", Text: "Contents\n1. Software Efficiency. . . . . . 9\n2. Conquering Efficiency. . . . . 21\n3. Efficiency Observability. . . 131"}
	if !IsPageMatter(toc) {
		t.Fatal("a table of contents must be matter")
	}
	content := entities.Part{Page: 12, Title: "Page 12", Text: "This page explains the Go memory model and how the allocator works."}
	if IsPageMatter(content) {
		t.Fatal("a content page with prose is not matter")
	}
}
