package partialentities

import "strings"

import "github.com/TheFranconianCoder/thought-keeper/internal/core/entities"

// PartialNode is the DTO for entities.SourceNode.
type PartialNode struct {
	NodeID    string
	ParentID  string
	Depth     int
	Ord       int
	Title     string
	Summary   string
	StartPage int
	EndPage   int
	Text      string
	OCR       bool
	// HasText marks a node that carries text of its own — i.e. a part a reader can open. A node
	// without text is a grouping heading (the document's title, a part), and the outline shows it as
	// one instead of offering it as something to read.
	HasText bool
}

// PartialFromNode converts a domain node into its partial.
func PartialFromNode(n entities.SourceNode) PartialNode {
	return PartialNode{
		NodeID:    n.NodeID,
		ParentID:  n.ParentID,
		Depth:     n.Depth,
		Ord:       n.Ord,
		Title:     n.Title,
		Summary:   n.Summary,
		StartPage: n.StartPage,
		EndPage:   n.EndPage,
		Text:      n.Text,
		OCR:       n.OCR,
		HasText:   n.HasText || strings.TrimSpace(n.Text) != "",
	}
}
