package usecases

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// ReconcileVocabulary makes the vocabulary files match the VAULT.
//
// The vault is the truth about which names EXIST (the claims carry them); the file is the
// truth about what they were CALLED before (the absorbed map). So "adopt what is there" is not a
// decision — it is writing down what is there — and dropping a canonical name that no longer occurs
// anywhere is the same act from the other side. It needs no model, and it is the repair half of the
// vocabulary the way `cleanup` is the repair half of the lint findings.
//
// Without it a vault has no baseline: every name would read as `new` forever, and the incremental part
// of a curation — ask only about what arrived since the last pass — could never work.
type ReconcileVocabulary struct {
	Report VocabularyReport
	Vocab  interfaces.VocabularyStore
	Clock  interfaces.Clock
	Log    *slog.Logger
}

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

// Execute reconciles every namespace, coarse to fine.
func (uc ReconcileVocabulary) Execute(ctx context.Context) (ReconcileVocabularyResult, error) {
	var res ReconcileVocabularyResult
	if uc.Vocab == nil {
		return res, fmt.Errorf("vocabulary: no store configured")
	}
	healths, err := uc.Report.Execute(ctx)
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
