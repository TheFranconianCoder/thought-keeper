package usecases

import (
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Favorites is the use case of the reader's star: "this belongs to me". A favorite is not a tag and
// not a claim, it has no lifecycle, and it never feeds search or the association layer. The source's
// <hash>.meta record and the page's frontmatter are the truth; the use case mirrors them.
type Favorites struct {
	Index interfaces.IndexRepository
	Shelf interfaces.ShelfStore
	Wiki  interfaces.WikiStore
	Clock interfaces.Clock
}

// ReadLater is the use case of the reader's queue and of the standing rules that fill it. An interest
// is a rule the reader wrote: a strong lexical match queues a NEW source, a weak one stays a
// suggestion in the source's rail. A reader who clicks the clock meant it, so the queue is always set
// by hand.
type ReadLater struct {
	Store   interfaces.InterestStore
	Counter interfaces.ShelfCounter
	Shelf   interfaces.ShelfStore
	Signals interfaces.InterestSignalReader
	Index   interfaces.IndexRepository
	Atoms   interfaces.AtomRepository
	Clock   interfaces.Clock
	// OnQueued is called for every source an interest queued, so the app can announce it. Nil is a
	// supported state: the queue is written anyway.
	OnQueued func(hash string)
}
