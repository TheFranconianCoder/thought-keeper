package usecases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/claims"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// ProgrammaticFindings runs the deterministic health checks over the CLAIMS and returns the findings
// (unsaved). It is shared by the lint run and by cleanup, so the findings list always reflects the
// current state after a repair.
//
// There are no page findings any more: no orphans, no untagged pages, no pages without provenance.
// A claim body carries no links, so there is no red link to report either. The association layer is a
// cache derived from the claims, so a stale cluster is repaired by the cluster pass, not reported as
// a defect.
func ProgrammaticFindings(
	ctx context.Context,
	lint interfaces.LintRepository,
	atoms interfaces.AtomRepository,
	runID ulid.ULID,
	now time.Time,
) []entities.LintFinding {
	findings := make([]entities.LintFinding, 0, 64)
	add := func(kind string, sev entities.FindingSeverity, sk entities.SubjectKind, sid, msg string) {
		findings = append(findings, entities.LintFinding{
			ID: ulid.Make(), RunID: runID, Layer: entities.FindingProgrammatic,
			Kind: kind, Severity: sev, SubjectKind: sk, SubjectID: sid, Message: msg, CreatedAt: now,
		})
	}

	if list, err := lint.AtomsWithoutSource(ctx, 1000); err == nil {
		for _, a := range list {
			add("provenance_gap", entities.SeverityWarning, entities.SubjectAtom, a.ID.String(),
				fmt.Sprintf("claim has no source: %s", a.Claim))
		}
	}
	if atoms != nil {
		// STREAMED, not materialised. This check visits every claim and runs after every cluster
		// pass (the settle) as well as on every lint run; reading all 170k claim texts into a slice
		// to walk them once was the last whole-corpus read left in the pipeline. A read failure is
		// swallowed, as before: findings are best-effort and a failed scan must not fail the pass.
		_ = atoms.StreamAtomTexts(ctx, func(a entities.Atom) error {
			if issues := claims.Issues(a.Topic, a.Claim, a.Body); len(issues) > 0 {
				add("not_standalone", entities.SeverityError, entities.SubjectAtom, a.ID.String(),
					fmt.Sprintf("%s — %s", a.Claim, strings.Join(issues, "; ")))
			}
			return nil
		})
	}
	findings = append(findings, vocabularyDrift(ctx, atoms, runID, now)...)
	return findings
}

// vocabularyDrift reports what the numbers can settle about the topic vocabulary without a judgement:
// two spellings of one name (always a bug), a few near-duplicate families (a curation question), a
// long tail that has grown past its use, and families too broad to merge at all. The report is
// AGGREGATED: one finding per category, never one per family. A list of 282 findings is not a health
// report; it is the vocabulary shouting.
func vocabularyDrift(ctx context.Context, atoms interfaces.AtomRepository, runID ulid.ULID, now time.Time) []entities.LintFinding {
	if atoms == nil {
		return nil
	}
	healths, err := VocabularyReport{Atoms: atoms}.Execute(ctx)
	if err != nil {
		return nil
	}
	var out []entities.LintFinding
	for _, h := range healths {
		if h.CollisionCount > 0 {
			out = append(out, entities.LintFinding{
				ID: ulid.Make(), RunID: runID, Layer: entities.FindingProgrammatic,
				Kind: h.Namespace + "_drift", Severity: entities.SeverityError,
				SubjectKind: entities.SubjectSource,
				Message: fmt.Sprintf("%s: %d names collapse to the same spelling — %s",
					h.Namespace, h.CollisionCount, sampleGroups(h.Collisions, 3)),
				CreatedAt: now,
			})
		}
		if h.NearDuplicateCount > 0 || h.OversizedFamilies > 0 {
			msg := fmt.Sprintf("%s: %d near-duplicate families", h.Namespace, h.NearDuplicateCount)
			if h.NearDuplicateCount > 0 {
				msg += " — " + sampleGroups(h.NearDuplicates, 3)
			}
			if h.OversizedFamilies > 0 {
				msg += fmt.Sprintf("; %d family/families (%d names) are too broad to merge and are left alone",
					h.OversizedFamilies, h.OversizedNames)
			}
			out = append(out, entities.LintFinding{
				ID: ulid.Make(), RunID: runID, Layer: entities.FindingProgrammatic,
				Kind: h.Namespace + "_drift", Severity: entities.SeverityInfo,
				SubjectKind: entities.SubjectSource, Message: msg, CreatedAt: now,
			})
		}
		if h.Names > 20 && h.SingletonShare > 0.6 {
			out = append(out, entities.LintFinding{
				ID: ulid.Make(), RunID: runID, Layer: entities.FindingProgrammatic,
				Kind: h.Namespace + "_drift", Severity: entities.SeverityWarning,
				SubjectKind: entities.SubjectSource,
				Message: fmt.Sprintf("%s: %d names, %d of them used once (%.0f%%) — the head carries %.0f%% of the uses",
					h.Namespace, h.Names, h.Singletons, h.SingletonShare*100, h.Coverage*100),
				CreatedAt: now,
			})
		}
	}
	return out
}

// sampleGroups renders at most max groups for an aggregated finding.
func sampleGroups(groups []partialentities.NameGroup, max int) string {
	if len(groups) == 0 {
		return "none"
	}
	parts := make([]string, 0, max)
	for i, g := range groups {
		if i == max {
			parts = append(parts, fmt.Sprintf("… and %d more", len(groups)-max))
			break
		}
		parts = append(parts, "["+groupNames(g)+"]")
	}
	return strings.Join(parts, " ")
}

// groupNames names a group's members for a finding. It is CAPPED: a group of names that share a word
// can hold a hundred members, and a finding nobody can read is a finding nobody acts on.
func groupNames(g partialentities.NameGroup) string {
	const max = 8
	parts := make([]string, 0, max)
	for i, n := range g.Names {
		if i == max {
			parts = append(parts, fmt.Sprintf("… and %d more", len(g.Names)-max))
			break
		}
		parts = append(parts, fmt.Sprintf("%q (%d)", n.Name, n.Uses))
	}
	return strings.Join(parts, ", ")
}
