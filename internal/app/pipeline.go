package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// The pipeline has four steps and ONE ordering rule:
//
//	capture → outline → distill → cluster
//
// capture, outline and distill are DOCUMENT steps: they run for one source, they may run for several
// sources at the same time, and they never touch the association layer. cluster is the VAULT-LEVEL
// step: it runs over the whole vault (every claim no cluster owns yet) and it may only run while no
// document step runs.
//
// That rule is a sync.RWMutex. A document step takes the read side, the cluster step takes the write
// side — and a writer that is waiting blocks new readers, so a due cluster pass cannot be starved by
// documents that keep arriving. Ask (a query) is never gated: it reads what exists right now, and a
// half-associated vault is still a vault worth asking.
type pipeCounters struct {
	// docRunning/docQueued count the document steps per step name.
	docRunning map[entities.Step]int
	docQueued  map[entities.Step]int
	// clusterRunning and clusterWaiting are the cluster runs: running holds the gate, waiting is
	// queued or blocked on the gate.
	clusterRunning int
	clusterWaiting int
	// asking is the number of query runs in flight (never gated).
	asking int
}

func (a *App) initPipe() {
	a.pipe = pipeCounters{
		docRunning: map[entities.Step]int{},
		docQueued:  map[entities.Step]int{},
	}
}

// enterDocument counts a document step as running and takes the shared side of the gate.
func (a *App) enterDocument(step entities.Step) {
	a.pipeMu.Lock()
	a.pipe.docRunning[step]++
	a.pipeMu.Unlock()
	a.publishPipeline()
}

func (a *App) leaveDocument(step entities.Step) {
	a.pipeMu.Lock()
	if a.pipe.docRunning[step] > 0 {
		a.pipe.docRunning[step]--
	}
	a.pipeMu.Unlock()
	a.publishPipeline()
}

func (a *App) queueDocument(step entities.Step, delta int) {
	a.pipeMu.Lock()
	a.pipe.docQueued[step] += delta
	if a.pipe.docQueued[step] < 0 {
		a.pipe.docQueued[step] = 0
	}
	a.pipeMu.Unlock()
	a.publishPipeline()
}

func (a *App) enterCluster() {
	a.pipeMu.Lock()
	a.pipe.clusterWaiting--
	if a.pipe.clusterWaiting < 0 {
		a.pipe.clusterWaiting = 0
	}
	a.pipe.clusterRunning++
	a.pipeMu.Unlock()
	a.publishPipeline()
}

func (a *App) leaveCluster() {
	a.pipeMu.Lock()
	if a.pipe.clusterRunning > 0 {
		a.pipe.clusterRunning--
	}
	a.pipeMu.Unlock()
	a.publishPipeline()
}

func (a *App) enterAsk() {
	a.pipeMu.Lock()
	a.pipe.asking++
	a.pipeMu.Unlock()
	a.publishPipeline()
}

func (a *App) leaveAsk() {
	a.pipeMu.Lock()
	if a.pipe.asking > 0 {
		a.pipe.asking--
	}
	a.pipeMu.Unlock()
	a.publishPipeline()
}

// PipelineStatus is the global status: which of the four steps is running, and how many documents
// are waiting for which step. The title bar shows it, so "what is the app doing?" has one answer.
func (a *App) PipelineStatus() payloads.Pipeline {
	// The jobs are collected BEFORE this lock is taken: Jobs reads the same pipe state, and a mutex is
	// not reentrant.
	jobs := a.Jobs()

	a.pipeMu.Lock()
	defer a.pipeMu.Unlock()
	p := payloads.Pipeline{
		Jobs:    jobs,
		Active:  "idle",
		Running: map[string]int{},
		Queued:  map[string]int{},
		Asking:  a.pipe.asking,
	}
	for _, step := range entities.PipelineSteps {
		if n := a.pipe.docRunning[step]; n > 0 {
			p.Running[string(step)] = n
		}
		if n := a.pipe.docQueued[step]; n > 0 {
			p.Queued[string(step)] = n
		}
	}
	if a.pipe.clusterRunning > 0 {
		p.Running[string(entities.StepCluster)] = a.pipe.clusterRunning
	}
	if a.pipe.clusterWaiting > 0 {
		p.Queued[string(entities.StepCluster)] = a.pipe.clusterWaiting
	}
	docs := 0
	for _, step := range entities.PipelineSteps {
		if step.IsDocument() {
			docs += p.Running[string(step)]
		}
	}
	switch {
	case p.Running[string(entities.StepCluster)] > 0:
		p.Active = "clusters"
	case docs > 0:
		p.Active = "documents"
	}
	return p
}

// Jobs lists what the app holds right now: every source a document step has taken on (or is waiting
// to take), the cluster step, and every ask and lint in flight.
func (a *App) Jobs() []payloads.Job {
	jobs := make([]payloads.Job, 0, 8)

	a.pipeMu.Lock()
	clusterRunning, clusterWaiting := a.pipe.clusterRunning, a.pipe.clusterWaiting
	asking := a.pipe.asking
	a.pipeMu.Unlock()

	// The document steps, per source.
	addSource := func(step, hash string, since time.Time) {
		job := payloads.Job{Step: step, State: "waiting", Target: hash}
		switch {
		case a.executingStep(hash) == entities.Step(step):
			job.State = "running"
		case a.isQueued(entities.Step(step), hash):
			job.State = "queued"
		default:
			// Taken, not executing: it waits for the vault's gate. Say WHO holds it — "the cluster
			// pass" was hardcoded and wrong whenever the consistency job, a curation or a maintenance
			// pass was the holder, which sends the reader looking at the wrong thing.
			job.Why = a.gateHolderReason("this step is next in line")
		}
		if !since.IsZero() {
			job.Since = since.UTC().Format(time.RFC3339)
		}
		if a.db != nil {
			job.Title = persistence.SourceTitle(a.db, hash)
		}
		jobs = append(jobs, job)
	}

	a.captureMu.Lock()
	captures := make([]string, 0, len(a.capturesInflight))
	for hash := range a.capturesInflight {
		captures = append(captures, hash)
	}
	a.captureMu.Unlock()
	sort.Strings(captures)
	for _, hash := range captures {
		addSource(string(entities.StepCapture), hash, time.Time{})
	}

	a.treeMu.Lock()
	trees := make([]string, 0, len(a.treesInflight))
	for hash := range a.treesInflight {
		trees = append(trees, hash)
	}
	a.treeMu.Unlock()
	sort.Strings(trees)
	for _, hash := range trees {
		addSource(string(entities.StepOutline), hash, time.Time{})
	}

	a.distillMu.Lock()
	distills := make([]string, 0, len(a.distillActive))
	for hash := range a.distillActive {
		distills = append(distills, hash)
	}
	sort.Strings(distills)
	claimed := make(map[string]time.Time, len(distills))
	for _, hash := range distills {
		claimed[hash] = a.claimedAt[hash]
	}
	a.distillMu.Unlock()
	for _, hash := range distills {
		addSource(string(entities.StepDistill), hash, claimed[hash])
	}

	// The cluster step runs over the whole vault, so it has no target. A maintenance run is a row of
	// its own below: a curation that asks the model does not hold the gate any more, so the cluster
	// pass can be reported beside it — and a reader who sees both is seeing the truth.
	maintenance := a.maintenanceJobs()
	if clusterRunning > 0 {
		jobs = append(jobs, payloads.Job{
			Step: string(entities.StepCluster), State: "running", Detail: a.clusterJobDetail(),
		})
	}
	if clusterWaiting > 0 {
		jobs = append(jobs, payloads.Job{
			Step: string(entities.StepCluster), State: "queued",
			Why: "waiting for the running document steps",
		})
	}
	if asking > 0 {
		jobs = append(jobs, payloads.Job{Step: "query", State: "running"})
	}
	if a.lintRunning > 0 {
		jobs = append(jobs, payloads.Job{Step: "lint", State: "running"})
	}
	jobs = append(jobs, maintenance...)
	// Running FIRST. A queue of a hundred waiting entries used to bury the one row that was actually
	// working, so the list read as "nothing happens".
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobRank(jobs[i]) < jobRank(jobs[j])
	})
	return jobs
}

// jobRank orders the Job Queue: what is happening now, then what waits for the gate, then what is
// queued behind the workers.
func jobRank(j payloads.Job) int {
	switch j.State {
	case "running":
		return 0
	case "waiting":
		return 1
	case "queued":
		return 2
	default:
		return 3
	}
}

// isQueued says whether this STEP of this source is waiting in a queue rather than executing.
func (a *App) isQueued(step entities.Step, hash string) bool {
	a.distillMu.Lock()
	defer a.distillMu.Unlock()
	return a.queued[queueKey(step, hash)]
}

// publishPipeline pushes the global status to the UI.
func (a *App) publishPipeline() {
	if a.hub == nil {
		return
	}
	a.hub.Publish("pipeline", map[string]any{"pipeline": a.PipelineStatus()})
}

// docStep runs fn as a DOCUMENT step.
func (a *App) docStep(ctx context.Context, step entities.Step, hash string, fn func(ctx context.Context) error) error {
	release := a.enterDocStep(step, hash)
	defer release()
	return fn(ctx)
}

// enterDocStep takes the shared gate and marks the source as executing that step.
//
// The step is marked BEFORE the counters are published: `enterDocument` publishes the pipeline status,
// and publishing first meant every frame said "queued"/"waiting" while a step was already running —
// the Job Queue could show nothing as running for minutes, which reads as a hang.
func (a *App) enterDocStep(step entities.Step, hash string) func() {
	a.gate.RLock()
	a.markExecuting(step, hash)
	a.enterDocument(step)
	return func() {
		a.clearExecuting(step, hash)
		a.leaveDocument(step)
		a.gate.RUnlock()
	}
}

// markExecuting records that a document step is really running for a source — inside the gate.
func (a *App) markExecuting(step entities.Step, hash string) {
	if hash == "" {
		return
	}
	a.execMu.Lock()
	defer a.execMu.Unlock()
	if a.executing == nil {
		a.executing = map[string]entities.Step{}
	}
	a.executing[hash] = step
}

func (a *App) clearExecuting(step entities.Step, hash string) {
	if hash == "" {
		return
	}
	a.execMu.Lock()
	defer a.execMu.Unlock()
	if a.executing[hash] == step {
		delete(a.executing, hash)
	}
}

// executingStep is the document step a source is executing right now, or "".
func (a *App) executingStep(hash string) entities.Step {
	a.execMu.Lock()
	defer a.execMu.Unlock()
	return a.executing[hash]
}

// clusterStep runs fn as the CLUSTER step: it waits for every running document step to end, blocks
// new ones while it runs, and starts no second cluster step.
func (a *App) clusterStep(ctx context.Context, waiting int, fn func(ctx context.Context) error) error {
	a.pipeMu.Lock()
	a.pipe.clusterWaiting += waiting
	a.pipeMu.Unlock()
	a.publishPipeline()

	a.gate.Lock()
	defer a.gate.Unlock()
	a.enterCluster()
	defer a.leaveCluster()
	return fn(ctx)
}

// ScheduleCluster queues the CLUSTER step (step 4) by hand — the one step that is steered globally
// rather than per source. It answers with the run id, or with an empty one when there is nothing to
// do (no claim without a cluster). Unlike a document run it never waits for a queue to be empty; the
// gate does that, and this returns as soon as the pass is queued.
func (a *App) ScheduleCluster() (string, error) {
	if !a.stepBarrierAllows(entities.StepCluster) {
		return "", types.New("conflict", "the cluster step waits until every source has finished distill", types.ErrConflict)
	}
	// A pass the user asked for clears the "no progress" state: their decision, not a loop.
	a.clusterMu.Lock()
	a.clusterStalled = 0
	a.clusterMu.Unlock()
	return a.requestCluster("manual")
}

// maxClusterNoProgress is how many cluster passes in a row may write nothing before the app stops
// asking for more. The automatic behaviour is bounded and says so.
const maxClusterNoProgress = 2

// reconcileWorker is the app's self-healing tick. Two jobs, both about the same promise — that the
// FILES are the truth:
//
//  1. Consistency: walk the vault and check that what the step records say is on disk. A deleted
//     outline (or a deleted rendering) invalidates everything derived from it; the source is
//     re-queued and derived again.
//  2. The cluster step: if there are claims no cluster owns yet, ask for a pass. That makes step 4
//     self-healing instead of depending on a particular trigger having fired.
//
// The walk is sliced (reconcile_batch per tick) and remembers where it stopped.
func (a *App) reconcileWorker() {
	interval, err := time.ParseDuration(a.cfg.Pipeline.ReconcileInterval)
	if err != nil || interval <= 0 {
		a.log.Info("consistency job disabled", "interval", a.cfg.Pipeline.ReconcileInterval)
		return
	}
	a.log.Info("consistency job enabled", "interval", interval.String(),
		"batch", a.cfg.Pipeline.ReconcileBatch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-ticker.C:
			a.reconcile("periodic")
		}
	}
}

// reconcile runs one consistency pass and then makes sure the cluster step is asked for.
//
// The walk runs in TWO phases, because of who else needs the gate. The check is read-only and takes no
// lock: a pass every minute that waited for a running document run (minutes, for a long book) would
// hold the gate's writer slot and stop every new reader — the pipeline would idle behind a job that
// only wanted to look. Only when the walk found something to repair does the real pass run, and that
// one takes the exclusive gate for the few sources it rewrites.
func (a *App) reconcile(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var res usecases.ReconcileResult
	var err error
	startOffset := a.reconcileOffset
	if a.reconcilePass.Index != nil {
		check := a.reconcilePass
		check.Offset = a.reconcileOffset
		check.DryRun = true
		res, err = check.Execute(ctx)
		if err == nil && res.Repairs > 0 {
			a.log.Info("consistency: repairs needed", "reason", reason,
				"invalidated", len(res.Invalidated), "broken", len(res.Broken),
				"languages", res.Languages, "identities", res.Identities, "ownerships", res.Ownerships)
			pass := a.reconcilePass
			pass.Offset = a.reconcileOffset
			a.gate.Lock()
			res, err = pass.Execute(ctx)
			a.gate.Unlock()
		}
	}
	a.reconcileOffset = res.Next
	if err == nil && a.reconcilePass.Index != nil {
		// The backlog the Status lens shows. A walk that starts at the first source starts a new
		// measurement; every slice of the walk adds what it found, so a sliced walk still shows the
		// whole picture instead of its last batch.
		if startOffset == 0 {
			a.resetSummaryMissing()
		}
		a.mergeSummaryMissing(res.MissingSummaries)
	}
	if err != nil {
		a.log.Warn("consistency check failed", "reason", reason, "err", err)
	} else {
		for _, hash := range res.Invalidated {
			a.log.Warn("consistency: source invalidated — deriving it again",
				"hash", hash, "reason", reason)
			a.publish("source", map[string]any{"hash": hash})
			a.enqueuePipeline(hash)
		}
		for _, hash := range res.Broken {
			a.log.Error("consistency: the source object is missing", "hash", hash)
			a.publish("source", map[string]any{"hash": hash})
		}
		if res.Languages > 0 || res.Identities > 0 || res.Ownerships > 0 {
			// The backfills changed rows that the reader may already be showing. A source event
			// re-reads the list and the open source, so the owner appears without a reload.
			a.publish("source", map[string]any{
				"languages": res.Languages, "identities": res.Identities, "ownerships": res.Ownerships,
			})
		}
		if len(res.Invalidated) > 0 || len(res.Broken) > 0 {
			a.publish("findings", map[string]any{"count": len(res.Invalidated) + len(res.Broken)})
		}
	}
	// The consistency walk found sources whose summary is missing. The pass itself never writes one
	// (a summary is a model call); the summary worker fills them in, a few per tick.
	a.requestSummaries(res.MissingSummaries)
	a.requestSourceTags(res.MissingSourceTags)
	a.pumpPipeline("reconcile")
	a.requestVocabularyIfDue(reason)
}

// requestClusterIfDue asks for a cluster pass when there is a backlog, and stops asking after two
// passes in a row that made no progress.
func (a *App) requestClusterIfDue(reason string) {
	if !a.stepBarrierAllows(entities.StepCluster) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srcs, err := a.pendingCluster.Execute(ctx)
	if err != nil {
		return
	}
	backlog := len(srcs)

	a.clusterMu.Lock()
	changed := backlog != a.clusterBacklog
	a.clusterBacklog = backlog
	stalled := a.clusterStalled
	if changed {
		a.clusterStalled = 0
		stalled = 0
	}
	a.clusterMu.Unlock()

	if backlog == 0 {
		return
	}
	if stalled >= maxClusterNoProgress {
		return
	}
	_, _ = a.requestCluster(reason)
}

// requestCluster asks for a cluster pass — but only when there is something to do. The backlog is
// the sources with claims no cluster owns yet; without the check every document run of a large
// import would create an empty run.
func (a *App) requestCluster(reason string) (string, error) {
	if !a.stepBarrierAllows(entities.StepCluster) {
		a.log.Debug("cluster pass waits until every source has finished distill", "reason", reason)
		return "", nil
	}
	a.requestClusterMu.Lock()
	defer a.requestClusterMu.Unlock()
	// One queued pass is enough. A pass that is RUNNING may have one successor queued, so work that
	// arrives while it runs is not lost.
	a.pipeMu.Lock()
	waiting := a.pipe.clusterWaiting
	a.pipeMu.Unlock()
	if waiting > 0 {
		a.log.Debug("cluster pass already queued", "reason", reason)
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srcs, err := a.pendingCluster.Execute(ctx)
	if err != nil {
		a.log.Warn("cluster: cannot read the backlog", "err", err)
		return "", err
	}
	if len(srcs) == 0 {
		a.log.Debug("cluster pass not needed", "reason", reason)
		return "", nil
	}
	runID, err := a.startRun.Execute(ctx, entities.RunPipeline, "", entities.StepCluster, "cluster")
	if err != nil {
		a.log.Warn("cluster: cannot start the run", "err", err)
		return "", err
	}
	job := runJob{runID: runID, op: entities.RunCluster, step: entities.StepCluster}
	select {
	case a.clusterQueue <- job:
		a.pipeMu.Lock()
		a.pipe.clusterWaiting++
		a.pipeMu.Unlock()
		a.publishPipeline()
		a.log.Info("cluster pass queued", "reason", reason, "sources", len(srcs), "run", runID.String())
		return runID.String(), nil
	default:
		// The slot was not there: close the run instead of leaving a `running` row behind.
		if a.startRun.Abandon(context.Background(), runID, "the cluster queue is full") != nil {
			a.log.Warn("cluster queue is full — the pass waits for the next trigger", "reason", reason)
		}
		return "", nil
	}
}

// clusterWorker runs the cluster step. It is a single worker on purpose: step 4 is considered over
// the whole vault, so two of them would be two answers to one question.
func (a *App) clusterWorker() {
	for {
		select {
		case <-a.quit:
			return
		case job := <-a.clusterQueue:
			a.runCluster(job)
		}
	}
}

// runCluster executes one cluster pass. It waits for the document steps to end (that is the gate)
// and then associates the backlog: the claims no cluster owns yet.
func (a *App) runCluster(job runJob) {
	run := job.runID.String()
	status, failure := "succeeded", ""
	defer a.clearClusterProgress()

	err := a.clusterStep(context.Background(), 0, func(context.Context) error {
		// A source may have been added after this pass was queued. Re-check the barrier INSIDE the
		// gate: if any source has not finished distill, step 4 must not run for the others.
		if !a.stepBarrierAllows(entities.StepCluster) {
			return fmt.Errorf("cluster pass deferred: not every source has finished distill")
		}
		// The budget starts INSIDE the gate: waiting for a long cluster pass is not work.
		ctx, cancel := a.runContextFor(job.op)
		defer cancel()
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		res, err := a.clusterPass.Execute(ctx, job.runID)
		if err != nil {
			return err
		}
		// The pass settles itself: dead members are pruned and the claim links resolved, so a
		// finished pass leaves no ghost trail and no stale membership.
		if a.cleanup.Clusters != nil {
			if _, cErr := a.cleanup.Execute(ctx); cErr != nil {
				a.log.Warn("cluster settle failed", "run", run, "err", cErr)
			}
		}
		a.log.Info("cluster pass done", "run", run,
			"new", res.New, "attached", res.Attached, "atoms", res.Atoms,
			"remaining", res.Remaining)
		// The pass is the app's memory peak: it reads every claim of the vault. Logging the heap
		// with it turns "step 4 feels heavy" into a number that can be compared between runs.
		mem := a.memoryReport()
		a.log.Info("cluster pass memory", "run", run,
			"heap_mb", mem.HeapMB, "heap_peak_mb", mem.PeakHeapMB, "sys_mb", mem.SysMB)
		a.clusterMu.Lock()
		if res.Atoms > 0 || res.New > 0 {
			a.clusterStalled = 0
		} else {
			a.clusterStalled++
			if a.clusterStalled >= maxClusterNoProgress {
				a.log.Warn("cluster pass makes no progress — not asking again until something changes",
					"stalled", a.clusterStalled, "remaining", res.Remaining)
			}
		}
		a.clusterMu.Unlock()
		a.publish("clusters", map[string]any{"clusters": res.New, "atoms": res.Atoms})
		if res.Remaining > 0 {
			a.log.Info("cluster pass continues", "remaining", res.Remaining)
			_, _ = a.requestCluster("continuation")
		}
		return nil
	})
	if err != nil {
		status, failure = "failed", err.Error()
		a.log.Warn("cluster pass failed", "run", run, "err", err)
	}
	a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": status, "error": failure})
	a.publish("clusters", map[string]any{"pass": "done"})
}
