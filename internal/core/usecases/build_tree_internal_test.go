package usecases

import (
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
