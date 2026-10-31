package usecases

import (
	"context"
	"fmt"
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// VocabularyNamespaceResult is one namespace's outcome.
type VocabularyNamespaceResult struct {
	Namespace string
	Canonical int
	Absorbed  int
	// Adopted are the names that were not canonical before (the vault has them, the file did not).
	Adopted int
	// Pruned are the canonical names that no longer occur anywhere, plus the absorbed entries whose
	// target is gone.
	Pruned int
	// Changed says whether the file was written.
	Changed bool
}

// ReconcileVocabularyResult reports every namespace.
type ReconcileVocabularyResult struct {
	Namespaces []VocabularyNamespaceResult
	Adopted    int
	Pruned     int
}

// Reconcile repairs the vocabularies against what the vault holds.
func (uc Vocabulary) Reconcile(ctx context.Context) (ReconcileVocabularyResult, error) {
	var res ReconcileVocabularyResult
	if uc.Vocab == nil {
		return res, fmt.Errorf("vocabulary: no store configured")
	}
	healths, err := uc.Report(ctx)
	if err != nil {
		return res, err
	}
	for _, h := range healths {
		ns := entities.VocabularyNamespace(h.Namespace)
		v, err := uc.Vocab.Read(ns)
		if err != nil {
			return res, err
		}
		observed := make(map[string]bool, len(h.Observed))
		for _, n := range h.Observed {
			observed[n.Name] = true
		}

		// The canonical set IS what the vault holds, in a stable order.
		canonical := make([]string, 0, len(h.Observed))
		known := make(map[string]bool, len(v.Canonical))
		for _, c := range v.Canonical {
			known[c] = true
		}
		names := make([]string, 0, len(observed))
		for name := range observed {
			names = append(names, name)
		}
		sort.Strings(names)
		canonical = append(canonical, names...)

		out := VocabularyNamespaceResult{Namespace: string(ns), Canonical: len(canonical)}
		for _, c := range canonical {
			if !known[c] {
				out.Adopted++
			}
		}
		for c := range known {
			if !observed[c] {
				out.Pruned++
			}
		}

		// The absorbed map is the one piece of knowledge here: keep it, minus the entries whose target
		// is gone (the name they were merged into does not exist any more, so the alias would point at
		// nothing).
		decided := make([]string, 0, len(v.Decided))
		for _, d := range v.Decided {
			if observed[d] {
				decided = append(decided, d)
			} else {
				out.Pruned++
			}
		}

		absorbed := map[string]string{}
		for from, to := range v.Absorbed {
			if !observed[to] {
				out.Pruned++
				continue
			}
			absorbed[from] = to
		}
		out.Absorbed = len(absorbed)

		out.Changed = out.Adopted > 0 || out.Pruned > 0 || len(v.Canonical) != len(canonical)
		if !out.Changed {
			res.Namespaces = append(res.Namespaces, out)
			continue
		}
		v.Canonical, v.Absorbed, v.Decided = canonical, absorbed, decided
		if uc.Clock != nil {
			v.Updated = uc.Clock.Now()
		}
		if err := uc.Vocab.Write(v); err != nil {
			return res, err
		}
		res.Adopted += out.Adopted
		res.Pruned += out.Pruned
		res.Namespaces = append(res.Namespaces, out)
	}
	if uc.Log != nil && (res.Adopted > 0 || res.Pruned > 0) {
		uc.Log.Info("vocabulary reconciled", "adopted", res.Adopted, "pruned", res.Pruned)
	}
	return res, nil
}
