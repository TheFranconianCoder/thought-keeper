package interfaces

import "github.com/TheFranconianCoder/thought-keeper/internal/core/entities"

// VocabularyStore reads and writes the vault's canonical name vocabularies (topics, branches, tags).
//
// A missing file is not an error: it is a vault whose names have never been curated, and the
// caller treats it as empty.
type VocabularyStore interface {
	Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error)
	Write(v entities.Vocabulary) error
}
