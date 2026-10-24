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

// resolveVocabularyList applies the alias table to a list, dropping empty and duplicate names.
func resolveVocabularyList(aliases map[string]string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = resolveVocabulary(aliases, name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}
