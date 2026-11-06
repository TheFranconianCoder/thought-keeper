package usecases

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestTreeChangedDecidesInvalidation: a rebuilt tree that is identical keeps the derivation (the
// common repair), a tree with other nodes, titles or text HASHES invalidates it. The text itself is
// compared as its hash: the outline does not carry the text.
func TestTreeChangedDecidesInvalidation(t *testing.T) {
	old := []entities.SourceNode{
		{NodeID: "0", Title: ""},
		{NodeID: "0.1", Title: "Chapter", TextHash: "hash-one"},
	}
	if treeChanged(nil, old) {
		t.Error("a first build must not invalidate anything")
	}
	if treeChanged(old, append([]entities.SourceNode{}, old...)) {
		t.Error("an identical rebuild must keep the derivation")
	}
	moved := []entities.SourceNode{{NodeID: "0", Title: ""}, {NodeID: "0.1.1", Title: "Chapter", TextHash: "hash-one"}}
	if !treeChanged(old, moved) {
		t.Error("node ids moved: the derivation is stale")
	}
	retitled := []entities.SourceNode{{NodeID: "0", Title: ""}, {NodeID: "0.1", Title: "Other", TextHash: "hash-one"}}
	if !treeChanged(old, retitled) {
		t.Error("a retitled node: the derivation is stale")
	}
	other := []entities.SourceNode{{NodeID: "0", Title: ""}, {NodeID: "0.1", Title: "Chapter", TextHash: "hash-two"}}
	if !treeChanged(old, other) {
		t.Error("other text: the derivation is stale")
	}
	dropped := []entities.SourceNode{{NodeID: "0", Title: ""}}
	if !treeChanged(old, dropped) {
		t.Error("a dropped node (boilerplate filter): the derivation is stale")
	}
}

// TestSameNodeTextTolerance: a normalized hash is only comparable with another normalized hash. An
// outline written before the field existed falls back to the exact hash — otherwise every node of
// every old outline would read as changed after the upgrade.
func TestSameNodeTextTolerance(t *testing.T) {
	a := entities.SourceNode{NodeID: "0.1", TextHash: "exact-a", TextNormHash: "norm"}
	b := entities.SourceNode{NodeID: "0.1", TextHash: "exact-b", TextNormHash: "norm"}
	if !sameNodeText(a, b) {
		t.Error("a reflowed node lost its tolerance")
	}
	c := entities.SourceNode{NodeID: "0.1", TextHash: "exact-c", TextNormHash: "other"}
	if sameNodeText(a, c) {
		t.Error("changed words were tolerated")
	}
	legacy := entities.SourceNode{NodeID: "0.1", TextHash: "exact-a"}
	if sameNodeText(legacy, b) {
		t.Error("an old exact hash was compared with a normalized one")
	}
	if !sameNodeText(legacy, entities.SourceNode{NodeID: "0.1", TextHash: "exact-a"}) {
		t.Error("two equal exact hashes were not equal")
	}
}

// TestNodeTextToleranceKeepsATinyEdit: the tolerance is a word-level similarity, not exact equality.
// One changed word in a long part keeps its claims; a paragraph that mostly changed is derived again.
func TestNodeTextToleranceKeepsATinyEdit(t *testing.T) {
	long := strings.Repeat("caching keeps a request fast and a budget predictable ", 20)
	tiny := strings.Replace(long, "fast", "quick", 1)
	a := entities.SourceNode{NodeID: "0.1", Text: long}
	b := entities.SourceNode{NodeID: "0.1", Text: tiny}
	if !sameNodeText(a, b) {
		t.Error("one changed word in a long part lost its claims")
	}
	other := strings.Repeat("an unrelated paragraph about tea and gardens ", 20)
	if sameNodeText(a, entities.SourceNode{NodeID: "0.1", Text: other}) {
		t.Error("an unrelated part kept its claims")
	}
}
