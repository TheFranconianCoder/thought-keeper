package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

// TestTheClusterStepNeverRunsBesideADocumentStep is the rule the whole pipeline rests on: capture,
// outline and distill run per source and may run at the same time, and the cluster step may only run
// while NO document step runs — in both directions. A cluster pass that started while a document was
// still deriving would group claims that are about to be replaced; a document step that started while
// the cluster pass was writing would change membership under the writer.
func TestTheClusterStepNeverRunsBesideADocumentStep(t *testing.T) {
	a := &App{}
	a.initPipe()
	a.log = nil // publishPipeline tolerates a nil hub; nothing here needs a logger
	ctx := context.Background()

	docStarted := make(chan struct{})
	docRelease := make(chan struct{})
	docDone := make(chan struct{})
	go func() {
		_ = a.docStep(ctx, entities.StepDistill, "aaaa", func(context.Context) error {
			close(docStarted)
			<-docRelease
			return nil
		})
		close(docDone)
	}()
	<-docStarted

	// A cluster step that is due must WAIT, not overlap.
	clusterStarted := make(chan struct{})
	clusterRelease := make(chan struct{})
	clusterDone := make(chan struct{})
	go func() {
		_ = a.clusterStep(ctx, 1, func(context.Context) error {
			close(clusterStarted)
			<-clusterRelease
			return nil
		})
		close(clusterDone)
	}()
	select {
	case <-clusterStarted:
		t.Fatal("the cluster step ran while a document step was running")
	case <-time.After(80 * time.Millisecond):
	}
	if got := a.PipelineStatus().Active; got != "documents" {
		t.Errorf("active = %q, want documents", got)
	}

	// Once the document step ends, the waiting cluster step goes — and while IT runs, a new document
	// step waits (the writer blocks new readers, so a due cluster pass cannot be starved).
	close(docRelease)
	<-docDone
	select {
	case <-clusterStarted:
	case <-time.After(time.Second):
		t.Fatal("the cluster step did not start once the documents were quiet")
	}

	newDocStarted := make(chan struct{})
	go func() {
		_ = a.docStep(ctx, entities.StepOutline, "bbbb", func(context.Context) error {
			close(newDocStarted)
			return nil
		})
	}()
	select {
	case <-newDocStarted:
		t.Fatal("a document step started while the cluster step was running")
	case <-time.After(80 * time.Millisecond):
	}
	close(clusterRelease)
	<-clusterDone
	select {
	case <-newDocStarted:
	case <-time.After(time.Second):
		t.Fatal("the document step did not start after the cluster step released the gate")
	}
}

// TestPipelineStatusNamesTheWorkingState: the title bar draws one of two working states, and the
// two never overlap. Ask runs beside both (it only reads).
func TestPipelineStatusNamesTheWorkingState(t *testing.T) {
	a := &App{}
	a.initPipe()
	if got := a.PipelineStatus(); got.Active != "idle" || got.Asking != 0 {
		t.Errorf("empty status = %+v, want idle", got)
	}

	a.enterDocument(entities.StepDistill)
	a.enterDocument(entities.StepOutline)
	a.enterAsk()
	got := a.PipelineStatus()
	if got.Active != "documents" {
		t.Errorf("active = %q, want documents", got.Active)
	}
	if got.Running["distill"] != 1 || got.Running["outline"] != 1 {
		t.Errorf("running = %v, want one outline and one distill", got.Running)
	}
	if got.Asking != 1 {
		t.Errorf("asking = %d, want 1: a query is never gated", got.Asking)
	}
	a.leaveAsk()

	a.enterCluster()
	if got := a.PipelineStatus(); got.Active != "clusters" {
		t.Errorf("active = %q, want clusters", got.Active)
	}
}

// A run a worker has TAKEN but that waits for the cluster pass must not be reported as running. The
// four steps forbid a document step beside the cluster step, and a list that shows both as `running`
// claims exactly that.
func TestATakenRunWaitingForTheClusterPassIsNotRunning(t *testing.T) {
	a := &App{}
	a.initPipe()
	a.distillActive = map[string]bool{"aaaa": true}
	a.claimedAt = map[string]time.Time{"aaaa": time.Now()}
	a.queued = map[string]bool{}
	a.executing = map[string]entities.Step{}

	// The cluster pass holds the gate; the distill run was taken by a worker but cannot enter.
	a.pipeMu.Lock()
	a.pipe.clusterRunning = 1
	a.pipeMu.Unlock()

	jobs := a.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("jobs = %+v, want the distill run and the cluster pass", jobs)
	}
	byStep := map[string]payloads.Job{}
	for _, j := range jobs {
		byStep[j.Step] = j
	}
	if got := byStep["cluster"]; got.State != "running" {
		t.Errorf("the cluster pass is not running: %+v", got)
	}
	distill, ok := byStep["distill"]
	if !ok {
		t.Fatalf("the distill run is missing: %+v", jobs)
	}
	if distill.State == "running" {
		t.Errorf("a run waiting for the cluster pass is reported as running: %+v", distill)
	}
	if distill.State != "waiting" || distill.Why == "" {
		t.Errorf("the waiting run does not say why: %+v", distill)
	}

	// Once it is inside the gate, it IS running.
	a.executing["aaaa"] = entities.StepDistill
	for _, j := range a.Jobs() {
		if j.Step == "distill" && j.State != "running" {
			t.Errorf("a run inside the gate is not running: %+v", j)
		}
	}
}

func TestTheJobQueueShowsClusterProgress(t *testing.T) {
	a := &App{}
	a.initPipe()
	a.pipeMu.Lock()
	a.pipe.clusterRunning = 1
	a.pipeMu.Unlock()
	a.setClusterProgress(usecases.ClusterProgress{
		Phase: "clustering", TopicsDone: 300, TopicsTotal: 4319,
		ClaimsDone: 1840, ClaimsTotal: 46066, Clusters: 37,
	})
	jobs := a.Jobs()
	if len(jobs) != 1 || jobs[0].Step != "cluster" {
		t.Fatalf("jobs = %+v, want the cluster pass", jobs)
	}
	if !strings.Contains(jobs[0].Detail, "300/4319 topics") ||
		!strings.Contains(jobs[0].Detail, "1840/46066 claims") ||
		!strings.Contains(jobs[0].Detail, "37 clusters") {
		t.Fatalf("cluster progress is missing from the job row: %+v", jobs[0])
	}
}

// TestJobQueueShowsARunningStepAsRunning: a step that IS executing must be reported as running, and the
// running row must come first. The queue used to publish its first frame BEFORE the step was marked
// executing, so every frame said "queued"/"waiting" while work was happening — a busy app that looked
// stuck for minutes.
func TestJobQueueShowsARunningStepAsRunning(t *testing.T) {
	a := &App{}
	a.initPipe()
	a.distillMu.Lock()
	a.queued = map[string]bool{}
	a.distillMu.Unlock()
	a.execMu.Lock()
	a.executing = map[string]entities.Step{}
	a.execMu.Unlock()

	// Three claimed sources, two of them merely queued.
	a.treeMu.Lock()
	a.treesInflight = map[string]bool{"aaaa": true, "bbbb": true, "cccc": true}
	a.treeMu.Unlock()
	a.distillMu.Lock()
	a.queued["bbbb"] = true
	a.queued["cccc"] = true
	a.distillMu.Unlock()

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = a.docStep(context.Background(), entities.StepOutline, "aaaa", func(context.Context) error {
			close(started)
			<-release
			return nil
		})
		close(done)
	}()
	<-started

	jobs := a.Jobs()
	if len(jobs) == 0 {
		t.Fatal("no jobs while an outline is executing")
	}
	if jobs[0].State != "running" || jobs[0].Target != "aaaa" || jobs[0].Step != "outline" {
		t.Fatalf("first job = %+v, want the running outline first", jobs[0])
	}
	for _, j := range jobs[1:] {
		if j.State == "running" {
			t.Fatalf("a second job claims to run: %+v", j)
		}
	}

	close(release)
	<-done
}

// TestWaitingJobNamesTheRealGateHolder: "the cluster pass is grouping claims" was hardcoded, so a job
// waiting for a curation or the consistency job sent the reader to the wrong place.
func TestWaitingJobNamesTheRealGateHolder(t *testing.T) {
	a := &App{}
	a.initPipe()
	if got := a.gateHolder(); got != "" {
		t.Fatalf("gateHolder = %q, want nobody", got)
	}
	a.pipeMu.Lock()
	a.pipe.clusterRunning = 1
	a.pipeMu.Unlock()
	if got := a.gateHolder(); !strings.Contains(got, "cluster") {
		t.Fatalf("gateHolder = %q, want the cluster pass", got)
	}
	a.pipeMu.Lock()
	a.pipe.clusterRunning = 0
	a.pipeMu.Unlock()

	a.maintMu.Lock()
	a.maint = map[ulid.ULID]maintJob{}
	id := ulid.Make()
	a.maint[id] = maintJob{label: "vocabulary", state: "running"}
	a.maintMu.Unlock()
	if got := a.gateHolder(); !strings.Contains(got, "vocabulary") {
		t.Fatalf("gateHolder = %q, want the vocabulary curation", got)
	}
	if got := a.gateHolderReason("this step is next"); !strings.Contains(got, "vocabulary") {
		t.Fatalf("reason = %q, want the real holder named", got)
	}
}

// TestTheRunningFrameIsPublishedWithTheStepAlreadyMarked is the regression the reader SAW: the first
// pipeline frame was published before the step was marked executing, so every frame said
// "queued"/"waiting" while work was happening. The frame that announces a step must already name it as
// running.
func TestTheRunningFrameIsPublishedWithTheStepAlreadyMarked(t *testing.T) {
	a := &App{}
	a.initPipe()
	a.hub = stream.NewHub()
	a.distillMu.Lock()
	a.queued = map[string]bool{}
	a.distillMu.Unlock()
	a.execMu.Lock()
	a.executing = map[string]entities.Step{}
	a.execMu.Unlock()
	a.treeMu.Lock()
	a.treesInflight = map[string]bool{"aaaa": true}
	a.treeMu.Unlock()
	a.distillMu.Lock()
	a.queued["aaaa"] = true
	a.distillMu.Unlock()

	sub := a.hub.Subscribe()
	defer a.hub.Unsubscribe(sub)

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = a.docStep(context.Background(), entities.StepOutline, "aaaa", func(context.Context) error {
			close(started)
			<-release
			return nil
		})
		close(done)
	}()
	<-started

	// Read the frames that were published while the step is running: at least one must show it running.
	found := false
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	for !found {
		msg, ok := sub.Next(ctx)
		if !ok {
			break
		}
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Pipeline struct {
					Jobs []struct {
						Step   string `json:"step"`
						State  string `json:"state"`
						Target string `json:"target"`
					} `json:"jobs"`
				} `json:"pipeline"`
			} `json:"data"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil || ev.Type != "pipeline" {
			continue
		}
		for _, j := range ev.Data.Pipeline.Jobs {
			if j.Step == "outline" && j.Target == "aaaa" && j.State == "running" {
				found = true
			}
		}
	}
	close(release)
	<-done
	if !found {
		t.Fatal("no published pipeline frame reported the executing outline as running")
	}
}
