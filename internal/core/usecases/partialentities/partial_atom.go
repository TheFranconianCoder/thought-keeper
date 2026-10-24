package partialentities

import (
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// PartialAtomSource is provenance from an atom to a source node.
type PartialAtomSource struct {
	Hash   string
	Title  string
	NodeID string
	OCR    bool
}

// PartialAtom is the DTO for entities.Atom.
type PartialAtom struct {
	ID        ulid.ULID
	Topic     string
	Path      string
	Claim     string
	Body      string
	Type      string
	Status    string
	Sources   []PartialAtomSource
	CreatedAt time.Time
	UpdatedAt time.Time
	// SourceCount is set on list queries where the full provenance is not loaded.
	SourceCount int
	// Clusters are the associations this claim belongs to. They are NOT listed in the UI (the
	// association layer has no titles and no navigation of its own); the Claims view uses them to
	// show a claim's cluster PEERS, which is how a reader follows the trail.
	Clusters []ulid.ULID
}

// ToEntity converts the partial into a domain entity.
func (p PartialAtom) ToEntity() entities.Atom {
	srcs := make([]entities.AtomSource, 0, len(p.Sources))
	for _, s := range p.Sources {
		srcs = append(srcs, entities.AtomSource{Hash: s.Hash, Title: s.Title, NodeID: s.NodeID, OCR: s.OCR})
	}
	return entities.Atom{
		ID: p.ID, Topic: p.Topic, Path: p.Path,
		Claim: p.Claim, Body: p.Body,
		Type: entities.AtomType(p.Type), Status: entities.AtomStatus(p.Status),
		Sources:   srcs,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// PartialFromAtom converts a domain entity into its partial.
func PartialFromAtom(a entities.Atom) PartialAtom {
	srcs := make([]PartialAtomSource, 0, len(a.Sources))
	for _, s := range a.Sources {
		srcs = append(srcs, PartialAtomSource{Hash: s.Hash, Title: s.Title, NodeID: s.NodeID, OCR: s.OCR})
	}
	return PartialAtom{
		ID: a.ID, Topic: a.Topic, Path: a.Path,
		Claim: a.Claim, Body: a.Body,
		Type: string(a.Type), Status: string(a.Status),
		Sources: srcs, SourceCount: a.SourceCount,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}
