package payloads

import (
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// TestFeedAndSitemapLightsAreSkippedNotPending: a feed or a sitemap is a LIST of sources. Capture
// fetched the list; the other three steps do not apply, and "pending" on them read as a document
// stuck after step 1. A legacy record that left them pending must not leak into the lights.
func TestFeedAndSitemapLightsAreSkippedNotPending(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	for _, tc := range []struct {
		name    string
		rss     bool
		sitemap bool
		detail  string
	}{
		{name: "feed", rss: true, detail: "feed"},
		{name: "sitemap", sitemap: true, detail: "sitemap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := partialentities.PartialSource{
				Hash: "aa", Kind: "url", Ext: ".md", Title: tc.name, RSS: tc.rss, Sitemap: tc.sitemap,
				Steps: entities.NewStepSet().
					With(entities.StepCapture, entities.StepStatus{State: entities.StepOK, At: at}),
			}
			wire := SourceFromPartial(src)
			if got := wire.Steps["capture"].State; got != string(entities.StepOK) {
				t.Errorf("capture light = %q, want ok", got)
			}
			for _, step := range []string{"outline", "distill", "cluster"} {
				st := wire.Steps[step]
				if st.State != string(entities.StepSkipped) {
					t.Errorf("%s light = %q, want skipped (does not apply to a list)", step, st.State)
				}
				if st.Detail != tc.detail {
					t.Errorf("%s detail = %q, want %q", step, st.Detail, tc.detail)
				}
			}
		})
	}
}

// TestDistillLightGoesAmberWhenRecordAndTreeDisagree: the step record can say `ok` while the tree has
// text-bearing parts no record covers (a recreated index, a lost part record, a tree that grew).
// Saying `ok` over "0 of 161 parts done" is the kind of number that makes nobody trust the lights —
// so the light turns amber and says how far the record really is.
func TestDistillLightGoesAmberWhenRecordAndTreeDisagree(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	src := partialentities.PartialSource{
		Hash: "aa", Kind: "md", Ext: ".md", Title: "Caching",
		TreeState:  string(entities.TreeBuilt),
		PartsTotal: 161, PartsDone: 0, AtomCount: 12,
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK, At: at}).
			With(entities.StepOutline, entities.StepStatus{State: entities.StepOK, At: at}).
			With(entities.StepDistill, entities.StepStatus{State: entities.StepOK, At: at}),
	}
	wire := SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepStale) {
		t.Errorf("distill light = %q, want stale when 0 of 161 parts are recorded", got)
	}
	if wire.Steps["distill"].Detail == "" {
		t.Error("the amber light must say how far the record is")
	}

	// A derivation that really is finished stays green.
	src.PartsDone = 161
	wire = SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepOK) {
		t.Errorf("distill light = %q, want ok when every part is recorded", got)
	}

	// A run that is still going is running, not stale.
	src.PartsDone = 40
	src.Steps = src.Steps.With(entities.StepDistill, entities.StepStatus{State: entities.StepRunning, At: at})
	wire = SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepRunning) {
		t.Errorf("distill light = %q, want running", got)
	}
}

// TestDistillLightIsEmptyUntilItBeginsAndNeverGreenWithoutAClaim: two rules the lights owe the reader.
// An outline whose parts have not been derived yet has NOT begun — that is an empty circle, not the
// amber "started, not finished". And a derivation that recorded its parts but produced no claim at all
// yielded nothing: it must never be painted green, because a part yields a claim, so "0 claims" means
// the derivation did not do its job.
func TestDistillLightIsEmptyUntilItBeginsAndNeverGreenWithoutAClaim(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	steps := entities.NewStepSet().
		With(entities.StepCapture, entities.StepStatus{State: entities.StepOK, At: at}).
		With(entities.StepOutline, entities.StepStatus{State: entities.StepOK, At: at})

	// Outlined, nothing derived: not begun, so the light is empty.
	src := partialentities.PartialSource{
		Hash: "cc", Kind: "md", Ext: ".md", Title: "Caching",
		TreeState: string(entities.TreeBuilt), PartsTotal: 12, PartsDone: 0, AtomCount: 0,
		Steps: steps,
	}
	wire := SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepPending) {
		t.Errorf("distill light = %q, want pending before anything was derived", got)
	}

	// The record says `ok` and every part is recorded — but no part produced a claim.
	src.PartsDone = 12
	src.PartsDerived = 12
	src.Steps = steps.With(entities.StepDistill, entities.StepStatus{State: entities.StepOK, At: at})
	wire = SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepStale) {
		t.Errorf("distill light = %q, want stale when parts recorded no claim", got)
	}
	if wire.Steps["distill"].Detail == "" {
		t.Error("the amber light must say why it is amber")
	}

	// Parts recorded in the part records but no record and no claim: also amber, never green.
	src.Steps = steps
	src.PartsDone = 4
	src.PartsDerived = 4
	wire = SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got == string(entities.StepOK) {
		t.Errorf("distill light = %q, want not green while the source has no claim", got)
	}

	// A claim exists and every part is recorded: done, green.
	src.AtomCount = 3
	src.PartsDone = 12
	src.PartsDerived = 12
	wire = SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepOK) {
		t.Errorf("distill light = %q, want ok with claims and every part recorded", got)
	}
}

// TestClusterLightIsAmberWhileClaimsAreOpen: the cluster step runs over the whole vault, so per source
// it is DERIVED — and a source whose claims do not all sit in a cluster has started but not finished
// the step. Empty means "not run", green means "done".
func TestClusterLightIsAmberWhileClaimsAreOpen(t *testing.T) {
	src := partialentities.PartialSource{
		Hash: "bb", Kind: "md", Ext: ".md", Title: "Caching",
		TreeState:  string(entities.TreeBuilt),
		PartsTotal: 12, PartsDone: 12, AtomCount: 9, AtomsLeft: 3,
	}
	wire := SourceFromPartial(src)
	if got := wire.Steps["cluster"].State; got != string(entities.StepStale) {
		t.Errorf("cluster light = %q, want stale while 3 claims have no cluster", got)
	}
	if wire.Steps["cluster"].Detail == "" {
		t.Error("the amber cluster light must say how many claims are open")
	}

	// Every claim on a page: done, green.
	src.AtomsLeft = 0
	wire = SourceFromPartial(src)
	if got := wire.Steps["cluster"].State; got != string(entities.StepOK) {
		t.Errorf("cluster light = %q, want ok when every claim is in a cluster", got)
	}

	// Claims exist but NONE is in a cluster: the cluster step has not started, so the light is empty
	// — not amber. This is the state after a fresh distill, before step 4 has done anything.
	src.AtomsLeft = src.AtomCount
	wire = SourceFromPartial(src)
	if got := wire.Steps["cluster"].State; got != string(entities.StepPending) {
		t.Errorf("cluster light = %q, want pending when no claim has reached a cluster", got)
	}

	// No claims at all, and the document steps are through: there is nothing to associate, so the
	// step is DONE for this source. An empty circle here waited for ever and read as "not clustered";
	// the association layer cannot group a claim the source never produced (a link list, metadata).
	src.AtomCount = 0
	src.AtomsLeft = 0
	wire = SourceFromPartial(src)
	if got := wire.Steps["cluster"].State; got != string(entities.StepOK) {
		t.Errorf("cluster light = %q, want ok when a finished source has no claims", got)
	}
	if wire.Steps["cluster"].Detail == "" {
		t.Error("the green cluster light must say why there was nothing to associate")
	}

	// A source with no claims that is NOT through its document steps is not done either: the
	// derivation owes it something, so the cluster light stays empty.
	src.PartsDerived = src.PartsTotal
	wire = SourceFromPartial(src)
	if got := wire.Steps["cluster"].State; got != string(entities.StepPending) {
		t.Errorf("cluster light = %q, want pending while the derivation is unfinished", got)
	}
}

// TestDistillLightIgnoresSourceRelativeParts: after the derived vault is deleted but the source's
// .meta survives, a reindex leaves the metadata parts (reason source_relative) done while every real
// part is incomplete. Two done metadata parts out of 537 are not evidence that distill began — the
// light must stay empty until a productive part or a claim exists.
func TestDistillLightIgnoresSourceRelativeParts(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	src := partialentities.PartialSource{
		Hash: "rust", Kind: "url", Ext: ".md", Title: "The Rust Programming Language",
		TreeState:  string(entities.TreeBuilt),
		PartsTotal: 537, PartsDone: 2, PartsDerived: 0, AtomCount: 0,
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK, At: at}).
			With(entities.StepOutline, entities.StepStatus{State: entities.StepOK, At: at}),
	}
	wire := SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepPending) {
		t.Fatalf("distill light = %q, want pending when only source_relative parts are done", got)
	}

	// A document consisting only of metadata is the explicit exception: it is complete.
	src.PartsTotal = 2
	src.PartsDone = 2
	wire = SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepOK) {
		t.Errorf("distill light = %q, want ok for a metadata-only document", got)
	}
	if wire.Steps["distill"].Detail == "" {
		t.Error("the metadata-only light should say why it has no claim")
	}
}

// TestClusterLightIsGreenForAFinishedSourceWithoutClaims is the source from the vault that started
// this: a wiki page that is only a list of links. Every document step is through, the one part is
// `source_relative`, there are no claims — and the cluster light used to sit on "not started" for ever.
func TestClusterLightIsGreenForAFinishedSourceWithoutClaims(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	src := partialentities.PartialSource{
		Hash: "ddceab288d", Kind: "md", Ext: ".md", Title: "37signals Employee Handbook",
		TreeState:  string(entities.TreeBuilt),
		PartsTotal: 1, PartsDone: 1, PartsDerived: 0, AtomCount: 0, AtomsLeft: 0,
		Steps: entities.NewStepSet().
			With(entities.StepCapture, entities.StepStatus{State: entities.StepOK, At: at}).
			With(entities.StepOutline, entities.StepStatus{State: entities.StepOK, At: at}).
			With(entities.StepDistill, entities.StepStatus{State: entities.StepOK, At: at}),
	}
	wire := SourceFromPartial(src)
	if got := wire.Steps["distill"].State; got != string(entities.StepOK) {
		t.Fatalf("distill light = %q, want ok (links/metadata are done without a claim)", got)
	}
	if got := wire.Steps["cluster"].State; got != string(entities.StepOK) {
		t.Fatalf("cluster light = %q, want ok for a finished source with nothing to associate", got)
	}
	if wire.Steps["cluster"].Detail == "" {
		t.Error("the green cluster light must say why there was nothing to associate")
	}
}
