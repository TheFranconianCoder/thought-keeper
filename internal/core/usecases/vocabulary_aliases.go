package usecases

import (
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// vocabularyAliases reads the alias table of one name namespace. The table is written by a
// curation: an absorbed name is mapped onto the canonical one that took it. Applying it at WRITE
// time is what stops the drift from reappearing on the next run (the curation would otherwise have
// to collect the same spelling again and again).
func vocabularyAliases(store interfaces.VocabularyStore, ns entities.VocabularyNamespace) map[string]string {
	if store == nil {
		return nil
	}
	v, err := store.Read(ns)
	if err != nil || len(v.Absorbed) == 0 {
		return nil
	}
	out := make(map[string]string, len(v.Absorbed))
	for from, to := range v.Absorbed {
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if from != "" && to != "" {
			out[from] = to
		}
	}
	return out
}

// resolveVocabulary maps a name through an alias table. An unknown name is itself.
func resolveVocabulary(aliases map[string]string, name string) string {
	if len(aliases) == 0 {
		return name
	}
	if to, ok := aliases[strings.TrimSpace(name)]; ok {
		return to
	}
	return name
}

// aliasesOntoShelf keeps only the aliases whose target is on the shelf, following a chain when the
// alias points at another absorbed name. When a shelf exists, an alias must not be a back door to a
// fine name: a claim is filed onto the bounded vocabulary, and an alias that lands outside it would
// undo the pass that just placed the claim. An alias that cannot be chained onto the shelf is dropped,
// so the catalog's budget path decides what to do with the proposal.
func aliasesOntoShelf(aliases map[string]string, shelf []string) map[string]string {
	if len(aliases) == 0 || len(shelf) == 0 {
		return aliases
	}
	onShelf := make(map[string]bool, len(shelf))
	for _, name := range shelf {
		onShelf[name] = true
	}
	const maxHops = 8
	out := make(map[string]string, len(aliases))
	for from, to := range aliases {
		current := to
		seen := make(map[string]bool, maxHops)
		for hop := 0; hop < maxHops && current != "" && !seen[current]; hop++ {
			if onShelf[current] {
				out[from] = current
				break
			}
			seen[current] = true
			next, ok := aliases[current]
			if !ok {
				break
			}
			current = next
		}
	}
	return out
}
