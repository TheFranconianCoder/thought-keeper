// Package app is the desktop composition root: it owns the lifecycle (config, vault
// lock, index, HTTP service, watcher, tray) and exposes the status report.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gofrs/flock"
	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/vaultgit"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
	"github.com/TheFranconianCoder/thought-keeper/internal/version"
)

// Options configures a new App.
type Options struct {
	Config   config.Config
	Path     string
	Log      *slog.Logger
	Headless bool
	// NoOpen keeps the shell (and its tray) but does not hand the address to the operating system: for
	// development, where the reader already has the UI open in their own browser (or the Vite dev
	// server proxies to it) and a second tab is noise.
	NoOpen bool
}

// App is the running desktop application.
type App struct {
	cfg      config.Config
	cfgPath  string
	log      *slog.Logger
	headless bool
	noOpen   bool

	db        *sql.DB
	lock      *flock.Flock
	runner    interfaces.ToolRunner
	intake    usecases.IntakeSource
	capture   usecases.CaptureSource
	buildTree usecases.BuildTree
	// captureQueue holds sources whose bytes are not readable (a PDF): step 1 extracts the text and
	// writes the rendering, and only then is the outline queued. One worker: extraction is CPU-bound
	// (and OCR is very much so), and the queue is drained in order.
	captureQueue chan string
	treeQueue    chan string
	// SourceLog is where the app records what IT did with a source (queued, waiting, stale claim
	// released). The pipeline writes its own steps there; the scheduler must too, or a run that
	// waits in a queue is indistinguishable from a hang.
	SourceLog interfaces.SourceLog
	// distillFirst is the queue a USER asked for (button, CLI, reset follow-up). A pressed run
	// must not wait behind sixty auto-continue runs, so the workers look here first.
	distillFirst chan runJob
	startRun     usecases.StartRun
	documentRun  usecases.DocumentRun
	// clusterPass is step 4 and clusterQueue its single worker's queue: the cluster step is
	// considered over the whole vault, so there is exactly one of it.
	clusterPass  usecases.ClusterPass
	cleanup      usecases.Cleanup
	reindex      usecases.ReindexVault
	clusterQueue chan runJob
	// reconcilePass is the consistency job; reconcileOffset is where its next slice starts.
	reconcilePass   usecases.ReconcileVault
	reconcileOffset int
	// clusterBacklog/clusterStalled bound the automatic cluster passes: two passes in a row without
	// progress stop the asking until something changes. requestClusterMu serialises the
	// CHECK-then-queue of a pass, so two triggers cannot both see "nothing queued".
	clusterMu        sync.Mutex
	requestClusterMu sync.Mutex
	clusterBacklog   int
	clusterStalled   int
	// gate is the pipeline's one ordering rule: document steps share it, the cluster step takes it
	// exclusively. See pipeline.go.
	gate   sync.RWMutex
	pipeMu sync.Mutex
	// pipe carries what the app is doing right now (per step, per source); the pump requests themselves
	// are coalesced through pumpWake.
	pipe           pipeCounters
	query          usecases.QueryKnowledge
	lint           usecases.LintKnowledge
	getSource      usecases.GetSource
	sourceHasAtoms usecases.SourceHasAtoms
	missingAtoms   usecases.SourcesWithoutAtoms
	pendingCluster usecases.SourcesWithUnclustered
	missingTrees   usecases.SourcesWithoutTree
	// steps is the source step tracker (the <hash>.meta records). The app corrects a record that says
	// a step is running when no run is behind it any more.
	steps     interfaces.StepTracker
	openParts usecases.SourcesWithOpenParts
	// retries counts transient failures per source, so a broken endpoint cannot become a hot loop.
	retryMu sync.Mutex
	retries map[string]int
	// lintMu guards the count of lint runs in flight (reported by /status).
	lintMu      sync.Mutex
	lintRunning int
	// pendingDeriv holds sources a reset wants re-derived once their running run has ended.
	derivMu      sync.Mutex
	pendingDeriv map[string]bool
	// stalled and progress make "continue" a bounded decision: a source that stopped making
	// progress is no longer re-derived by itself.
	stalled       map[string]int
	progress      map[string]runProgress
	refreshURLs   usecases.RefreshURLs
	addURL        usecases.AddURL
	queueURLs     usecases.QueueURLs
	markURLFailed usecases.MarkURLFailed
	// tagPage derives a wiki page's tags; tagQueue hands that work to one worker off the request path.
	tagPage  usecases.TagWikiPage
	tagQueue chan runJob
	// summary writes a source's overview; summaryQueue gives that one worker of its own, and
	// summaryActive dedupes schedules so a finished document run and a consistency pass cannot queue
	// the same source twice.
	summary       usecases.SourceSummary
	tagSource     usecases.TagSource
	summaryQueue  chan runJob
	summaryMu     sync.Mutex
	summaryActive map[string]bool
	// summaryRemaining is the backlog the consistency walk measured (a set, so a completed summary
	// leaves it before the next walk) and summaryRunning is what the summary worker executes right
	// now. Both feed the Status lens while the backfill runs.
	summaryRemaining   map[string]bool
	summaryRunning     map[string]bool
	summaryRemainingAt time.Time
	// sourceTagActive dedupes the auto-tag derivation of a source (after a distill and from the
	// consistency backfill can both ask for it).
	sourceTagMu     sync.Mutex
	sourceTagActive map[string]bool
	distillQueue    chan runJob
	queryQueue      chan runJob
	lintQueue       chan runJob
	urlQueue        chan urlJob
	// pumpWake is the coalesced request for a pipeline pass. A wiki page that queues 150 links would
	// otherwise ask for 150 passes, and a pass walks every source: that is what made one save take
	// minutes (and made it quadratic in the size of the vault).
	pumpWake chan string
	// curate is the name-vocabulary curation: it asks the model and applies the answer.
	curate          usecases.CurateVocabulary
	rebranch        usecases.RebranchClaims
	vocabularyQueue chan runJob
	// The automatic vocabulary pass: at most one at a time, at most every vocabularyCooldown.
	vocabMu     sync.Mutex
	vocabActive bool
	vocabLast   time.Time
	// maint is every background job that is NOT one of the four steps: a maintenance pass the reader
	// can watch. The four steps are reported from their own state (per source, per gate); everything
	// else — a reindex, a reconcile, a cleanup, a rebuild of the outlines, a URL refresh, a name
	// curation — has no such state, and without this it ran invisibly.
	maintMu sync.Mutex
	maint   map[ulid.ULID]maintJob
	// clusterProgress is the live progress of the cluster pass. It is a report for the Job Queue,
	// not persisted state.
	clusterProgressMu sync.Mutex
	clusterProgress   usecases.ClusterProgress
	// clusterPublished is the last moment a clustering progress frame was pushed to the SSE hub, and
	// clusterPhase its phase. A naming run reports per batch (thousands of frames); the Status page
	// needs the Job Queue to move, not to reload /pipeline 600 times. So a frame is published at most
	// once every clusterPublishEvery, and immediately when the phase changes or the run completes.
	clusterPublished time.Time
	clusterPhase     string
	// vocabularyReconcile adopts the baseline (the names that exist) before a curation asks the
	// model: the vault is the truth about which names there are, so that is not a decision.
	vocabularyReconcile usecases.ReconcileVocabulary

	hub *stream.Hub

	agentAvailable bool

	addr          string
	handler       http.Handler
	srv           *http.Server
	startedAt     time.Time
	fts5          bool
	pdfOK         bool
	ocrOK         bool
	vaultWritable bool
	// schemaOutdated is the sentence a stale index gets: empty when the index has the shape this build
	// writes (or when it is brand new). It is REPORTED in the status report and in the start-up log,
	// never repaired by the app — see persistence.SchemaStatus.
	schemaOutdated string

	mu            sync.Mutex
	watcherPaused bool
	// inboxWatched/inboxError say whether the drop zone is on inotify or only polled (and why).
	inboxWatched bool
	inboxError   string
	// dropsInflight keeps ONE intake per dropped file, however many events arrive for it.
	dropMu        sync.Mutex
	dropsInflight map[string]bool

	distillMu     sync.Mutex
	distillActive map[string]bool
	claimedAt     map[string]time.Time
	// executing says which DOCUMENT step a source is really running right now. A claim covers waiting
	// and executing (that is what a claim is for); this is the part that is inside the gate — and only
	// that part may be reported as running, or a run waiting for the cluster pass looks like it runs
	// beside it.
	execMu    sync.Mutex
	executing map[string]entities.Step
	// queued is "waiting in a queue", scoped to ONE STEP of one source (see queueKey). One flag per
	// source let the tree worker's clearWaiting erase the distill's wait, and the watchdog then read a
	// live claim as stale.
	queued map[string]bool
	// claimedRun is the run row behind a distill claim, so a released claim can close the run it left
	// behind instead of leaving a "running" row nobody works on.
	claimedRun    map[string]ulid.ULID
	treesInflight map[string]bool
	treeMu        sync.Mutex
	// capturesInflight keeps a source from being captured twice (the add path and the start-up sweep
	// can both see "no rendering yet").
	capturesInflight map[string]bool
	captureMu        sync.Mutex
	maintenance      interfaces.Maintenance
	resetSource      usecases.ResetSource
	pruneWikiLinks   usecases.PruneWikiLinks

	quitOnce sync.Once
	quit     chan struct{}
	// memPeak is the highest live heap the app has OBSERVED. The runtime reports no peak, and the
	// association step spikes its heap inside one run and frees it before the next status read:
	// without a watermark of its own, the app could never answer "how much did a pass cost".
	memPeak atomic.Uint64
	// memPeakWork and memPeakAt NAME the watermark: a peak without an attribution is a number that
	// starts an argument instead of ending one. They say which work was running when the heap was
	// highest, and when that was.
	memPeakMu   sync.Mutex
	memPeakWork string
	memPeakAt   time.Time
	// memLogPeak is the last watermark that was WRITTEN to the log. A pass ramps in 2-second steps;
	// logging every step says the same thing twenty times. A new 64 MB step or a change of work is
	// what a reader can act on.
	memLogPeak uint64
	memLogWork string
	closeOnce  sync.Once
}

// New opens the vault and index, runs migrations and wires the HTTP handler. It
// does not start listening — that is Run's job.
func New(opts Options) (*App, error) {
	cfg := opts.Config
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	dirs := []string{
		cfg.VaultRoot(), cfg.SourcesDir(), cfg.InboxDir(), cfg.AtomsDir(), cfg.ClustersDir(),
		filepath.Dir(cfg.IndexDB()), cfg.FaviconsDir(), config.RuntimeDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}

	lockPath := filepath.Join(config.RuntimeDir(), "vault.lock")
	fl := flock.New(lockPath)
	ok, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire vault lock: %w", err)
	}
	if !ok {
		return nil, types.ErrLocked
	}

	db, err := persistence.Open(cfg.IndexDB())
	if err != nil {
		_ = fl.Unlock()
		return nil, err
	}
	if err := persistence.Migrate(db); err != nil {
		_ = db.Close()
		_ = fl.Unlock()
		return nil, err
	}
	// The shape of the index, checked and REPORTED. goose cannot notice a database written by an older
	// build (it does not run an applied migration again), and the failure it produces names a column
	// instead of the reason — every write dies on a constraint, every read on a column list. One line
	// here turns that into something the user can act on, and the app touches nothing: the index lives
	// in the vault, and deleting it is their decision.
	schemaOutdated := ""
	if st, err := persistence.InspectSchema(db); err != nil {
		log.Warn("could not read the index schema marker", "err", err)
	} else if st.Outdated() {
		schemaOutdated = st.Fix(cfg.IndexDB())
		log.Error("index schema is out of date",
			"file", cfg.IndexDB(), "found", st.Version, "want", st.Expected, "fix", "delete it and reindex")
	}

	a := &App{
		cfg:              cfg,
		cfgPath:          opts.Path,
		log:              log,
		headless:         opts.Headless || !cfg.App.Tray,
		noOpen:           opts.NoOpen,
		db:               db,
		lock:             fl,
		runner:           gateways.ExecRunner{},
		addr:             cfg.Server.Addr,
		startedAt:        time.Now(),
		vaultWritable:    checkWritable(cfg.VaultRoot()),
		schemaOutdated:   schemaOutdated,
		treeQueue:        make(chan string, 256),
		captureQueue:     make(chan string, 256),
		distillQueue:     make(chan runJob, 64),
		clusterQueue:     make(chan runJob, 16),
		distillFirst:     make(chan runJob, 16),
		queryQueue:       make(chan runJob, 16),
		lintQueue:        make(chan runJob, 16),
		tagQueue:         make(chan runJob, 8),
		summaryQueue:     make(chan runJob, 16),
		summaryActive:    make(map[string]bool),
		summaryRemaining: make(map[string]bool),
		summaryRunning:   make(map[string]bool),
		sourceTagActive:  make(map[string]bool),
		vocabularyQueue:  make(chan runJob, 4),
		// The first automatic curation waits one cooldown: a fresh start has work to do before the
		// names are worth a pass, and a seven-minute model call at minute one is not a good morning.
		vocabLast:        time.Now(),
		urlQueue:         make(chan urlJob, 4096),
		pumpWake:         make(chan string, 1),
		hub:              stream.NewHub(),
		retries:          make(map[string]int),
		pendingDeriv:     make(map[string]bool),
		stalled:          make(map[string]int),
		progress:         make(map[string]runProgress),
		distillActive:    make(map[string]bool),
		claimedAt:        make(map[string]time.Time),
		executing:        make(map[string]entities.Step),
		queued:           make(map[string]bool),
		claimedRun:       make(map[string]ulid.ULID),
		maint:            make(map[ulid.ULID]maintJob),
		treesInflight:    make(map[string]bool),
		capturesInflight: make(map[string]bool),
		quit:             make(chan struct{}),
	}
	a.initPipe()
	// A vault that is under version control must not collect derived-file churn: the app keeps the
	// ignore rules in place (append-only, never touching a user's own lines). A no-op for a vault
	// that is not a repository, and switchable with vault.git.ignore_derived.
	if cfg.Vault.Git.IgnoreDerived && vaultgit.IsRepo(cfg.VaultRoot()) {
		if n, err := vaultgit.EnsureIgnore(cfg.VaultRoot(), vaultgit.Patterns()); err != nil {
			log.Warn("could not update the vault's .gitignore", "err", err)
		} else if n > 0 {
			log.Info("vault .gitignore updated: derived files are excluded", "rules", n)
		} else {
			log.Debug("vault .gitignore is current")
		}
	}

	a.fts5 = persistence.EnsureFTS(db, a.log)
	a.pdfOK = a.runner.Available(cfg.PDF.TextTool)
	a.ocrOK = a.runner.Available(cfg.OCR.Rasterizer) && a.runner.Available(cfg.OCR.Tesseract)

	reg := registry.Build(cfg, db, a, a.onAdded, a.onRemoved, a.ScheduleWikiTag, a.ScheduleWikiReprocess, a.ScheduleWikiCleanup, a.ScheduleDiscoveredChildren, a, a, a, a, a, a.hub, a.log)
	a.handler = reg.Handler
	a.intake = reg.Intake
	a.capture = reg.Capture
	a.buildTree = reg.BuildTree
	a.startRun = reg.StartRun
	a.documentRun = reg.DocumentRun
	a.query = reg.Query
	a.lint = reg.Lint
	a.refreshURLs = reg.RefreshURLs
	a.addURL = reg.AddURL
	a.queueURLs = reg.QueueURLs
	a.markURLFailed = reg.MarkURLFailed
	a.tagPage = reg.TagWikiPage
	a.summary = reg.SourceSummary
	a.tagSource = reg.TagSource
	a.curate = reg.VocabularyCurate
	a.rebranch = reg.RebranchClaims
	a.vocabularyReconcile = reg.VocabularyReconcile
	a.getSource = reg.GetSource
	a.sourceHasAtoms = reg.SourceHasAtoms
	a.missingAtoms = reg.SourcesWithoutAtoms
	a.pendingCluster = reg.SourcesWithUnclustered
	a.SourceLog = reg.SourceLog
	a.openParts = reg.SourcesWithOpenParts
	a.missingTrees = reg.SourcesWithoutTree
	a.steps = reg.Steps
	a.agentAvailable = reg.AgentAvailable
	a.maintenance = reg.Maintenance
	a.resetSource = reg.ResetSource
	a.pruneWikiLinks = reg.PruneWikiLinks
	a.clusterPass = reg.ClusterPass
	a.clusterPass.Progress = a.setClusterProgress
	a.cleanup = reg.Cleanup
	a.reindex = reg.Reindex
	a.reconcilePass = reg.Reconcile
	return a, nil
}

// Shared wraps a synchronous request that only writes on the DOCUMENT side of the vault (an
// intake, a URL snapshot, a source-record repair). It takes the shared side of the gate, so it may
// run beside a document step but never beside the cluster step.
func (a *App) Shared(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.gate.RLock()
		defer a.gate.RUnlock()
		next.ServeHTTP(w, r)
	})
}

// Exclusive wraps a synchronous request that rewrites claims, pages or the index. It takes the
// exclusive side of the gate: it waits for every running document step and the cluster pass, and no
// new step starts while it works. This is what makes the four-step rule hold for the maintenance
// buttons too, not only for the workers.
func (a *App) Exclusive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.gate.Lock()
		defer a.gate.Unlock()
		next.ServeHTTP(w, r)
	})
}

// treeRunning reports whether a tree build for this source is queued or in flight.
func (a *App) treeRunning(hash string) bool {
	a.treeMu.Lock()
	defer a.treeMu.Unlock()
	return a.treesInflight[hash]
}

// Reset throws a source's derivation away and starts the pipeline over: the tree is built
// again, then the automatic distill derives the claims, exactly as after an add.
func (a *App) Reset(hash string) (usecases.ResetResult, error) {
	// A TREE BUILD that is EXECUTING would write its nodes after the reset. The exclusive gate
	// already keeps one from starting beside the reset; this covers a caller that reaches Reset
	// without the gate (a test, a future CLI path). A merely QUEUED tree is fine: it runs after the
	// reset and builds exactly the tree the reset asked for.
	if a.executingStep(hash) == entities.StepOutline {
		a.log.Info("reset: waiting for the running tree build", "hash", hash)
		for i := 0; i < 60 && a.executingStep(hash) == entities.StepOutline; i++ {
			time.Sleep(50 * time.Millisecond)
		}
	}
	res, err := a.resetSource.Execute(context.Background(), hash)
	if err != nil {
		return res, err
	}
	// A run for this source may still be writing what the reset just swept away. It cannot be
	// cancelled from here, so mark it: the run's own end queues a FRESH derivation.
	if !a.distillFree(hash) {
		a.markPendingDerivation(hash)
		a.log.Info("reset: a run is in flight — a fresh derivation follows it", "hash", hash)
	}
	a.publish("source", map[string]string{"hash": hash})
	if !res.Bookmark {
		a.pumpPipeline("source reset")
	}
	return res, nil
}

// onAdded reacts to a freshly stored source (watcher, POST /add, upload, URL):
// it notifies subscribers and schedules tree building off the request path.
func (a *App) onAdded(hash string) {
	a.hub.Publish("source", map[string]string{"hash": hash})
	a.pumpPipeline("source added")
}

// enqueuePipeline starts a source's document steps at step 1: a source whose bytes are not readable
// (a PDF) is captured first, and the capture hands over to the outline. For Markdown, text and a URL
// snapshot the intake WAS the capture, so the outline starts right away — the steps stay in order,
// the cheap ones just do not need a queue entry of their own.
func (a *App) enqueuePipeline(hash string) {
	// A source does not run ahead of the vault: the global pump decides which step is current and
	// queues every source that still owes that step. `hash` is kept for the caller's intent (and for
	// the log), but the barrier is global by design.
	_ = hash
	a.pumpPipeline("enqueue pipeline")
}

// needsCapture reports whether step 1 still has to read this source's bytes.
func (a *App) needsCapture(hash string) bool {
	src, err := a.getSource.Execute(context.Background(), hash)
	if err != nil {
		return false
	}
	return src.ToEntity().NeedsCapture()
}

// stepBarrierAllows reports whether EVERY source has completed every earlier document step.
// It is the global half of the four-step rule: a later step may not start while one source is
// still behind. Bookmarked sources are complete for outline/distill (they are skipped), and a
// readable kind (Markdown, text, a URL snapshot) is captured by intake, so no extraction is owed.
func (a *App) stepBarrierAllows(step entities.Step) bool {
	if a.steps == nil {
		return true
	}
	records, err := a.steps.Records()
	if err != nil || len(records) == 0 {
		return true
	}
	known := map[string]bool{}
	if a.getSource.Index != nil {
		if hashes, hErr := a.getSource.Index.SourceHashes(context.Background()); hErr == nil {
			known = make(map[string]bool, len(hashes))
			for _, h := range hashes {
				known[h] = true
			}
		}
	}
	for hash, rec := range records {
		if len(known) > 0 && !known[hash] {
			continue // a sidecar without its source is stale, not a barrier
		}
		for _, prior := range entities.PipelineSteps {
			if prior == step {
				break
			}
			if !a.stepComplete(hash, rec, prior) {
				return false
			}
		}
	}
	return true
}

// stepComplete reports whether one source has completed one step (ok or skipped).
func (a *App) stepComplete(hash string, rec entities.SourceRecord, step entities.Step) bool {
	// A feed or a sitemap is a LIST of sources, not a document. Its capture is the fetch that
	// produced the list; the outline, distill and association steps do not apply to it. No record —
	// a legacy one, a half-refresh — may hold the whole vault's barrier for a list.
	if rec.Identity.RSS || rec.Identity.Sitemap {
		return true
	}
	kind := rec.Identity.Kind
	if kind == "" && a.getSource.Index != nil {
		if src, err := a.getSource.Execute(context.Background(), hash); err == nil {
			kind = entities.SourceKind(src.Kind)
		}
	}
	if step == entities.StepCapture {
		if kind == "" {
			if src, err := a.getSource.Execute(context.Background(), hash); err == nil {
				kind = entities.SourceKind(src.Kind)
				if src.ToEntity().NeedsCapture() {
					goto captureRecorded
				}
			}
		}
		// Intake is the capture for Markdown, text and URL snapshots. A wiki page is the
		// exception: it is Markdown that the user owns and that is re-processed from capture on
		// every edit, even though its source kind is still "md".
		//
		// A URL a wiki page declared is the other exception to "intake was the capture": its row
		// exists before the fetch answers, so what its capture light says (pending, or failed with the
		// reason) IS the report — but it is not work the pipeline may redo. Its outline and distill are
		// recorded skipped for exactly that reason, so nothing downstream waits for text that is not
		// there.
		if !kind.NeedsCapture() && !entities.IsWikiOriginalName(rec.Identity.OriginalName) {
			return true
		}
	}
captureRecorded:
	st := rec.Steps.State(step)
	if st != entities.StepOK && st != entities.StepSkipped {
		return false
	}
	if step == entities.StepDistill && st == entities.StepOK && a.sourceHasAtoms.Index != nil {
		// The step record survived, but its RESULT may not: a vault that kept sources/ and
		// deleted atoms/ has a `.meta` that says "distilled" with no claims on disk. The record is
		// not allowed to claim a derivation that left nothing — treat it as open and derive again.
		if has, err := a.sourceHasAtoms.Execute(context.Background(), hash); err == nil && !has {
			// A part that carried only document metadata was finished as source_relative and correctly
			// produced no atom. If every recorded part is such a part, the derivation is complete.
			if len(rec.Parts) > 0 {
				onlySourceRelative := true
				for _, part := range rec.Parts {
					if part.State != entities.PartDone || part.Reason != "source_relative" {
						onlySourceRelative = false
						break
					}
				}
				if onlySourceRelative {
					return true
				}
			}
			// The record lost its part records (a reset written before that rule existed, a legacy
			// vault). The index still remembers how the derivation ended: no open text part means
			// the derivation is complete, and one ambiguous record must not hold the whole vault at
			// the distill barrier.
			if a.getSource.Index != nil {
				if open, oErr := a.getSource.Index.SourceOpenParts(context.Background(), hash); oErr == nil && open == 0 {
					return true
				}
			}
			return false
		}
	}
	return true
}

// pumpPipeline asks for a pipeline pass. The request is COALESCED and asynchronous: one pass walks
// every source, so a burst of one request per source — a wiki page that queues 150 links, each
// finishing separately — must not become 150 passes. Nobody who asks waits for the scan.
func (a *App) pumpPipeline(reason string) {
	if a.pumpWake == nil {
		return
	}
	select {
	case a.pumpWake <- reason:
	default:
		// A pass is already requested: one more reason changes nothing a pass would not already see.
	}
}

// pumpWorker runs the coalesced passes, one at a time and only after a quiet moment, so a burst is one
// pass with every new source already in the vault.
func (a *App) pumpWorker() {
	const quiet = 350 * time.Millisecond
	for {
		select {
		case <-a.quit:
			return
		case reason := <-a.pumpWake:
			timer := time.NewTimer(quiet)
			draining := true
			for draining {
				select {
				case <-a.pumpWake:
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(quiet)
				case <-timer.C:
					draining = false
				case <-a.quit:
					timer.Stop()
					return
				}
			}
			a.pumpOnce(reason)
		}
	}
}

// pumpOnce is one pass over the vault: it finds the first step not yet complete for every source,
// queues ONLY that step for the sources that still owe it, and returns. It never jumps a step, even
// when a source has work left in a later one.
func (a *App) pumpOnce(reason string) {
	if a.steps == nil {
		return
	}
	records, err := a.steps.Records()
	if err != nil || len(records) == 0 {
		return
	}
	// Which hashes the index has. A sidecar without its source is stale, not a barrier — and asking
	// for one source's whole detail (with its derived counts) per hash and step is what made one pass
	// cost almost a second on a vault of a few hundred sources.
	known := map[string]bool{}
	if a.getSource.Index != nil {
		if hashes, hErr := a.getSource.Index.SourceHashes(context.Background()); hErr == nil {
			known = make(map[string]bool, len(hashes))
			for _, h := range hashes {
				known[h] = true
			}
		}
	}
	for _, step := range []entities.Step{entities.StepCapture, entities.StepOutline, entities.StepDistill} {
		all := true
		for hash, rec := range records {
			if len(known) > 0 && !known[hash] {
				continue
			}
			if !a.stepComplete(hash, rec, step) {
				all = false
				break
			}
		}
		if all {
			continue
		}
		if step == entities.StepDistill && !a.cfg.Pipeline.AutoDistill {
			return
		}
		a.log.Info("pipeline barrier", "current_step", string(step), "reason", reason)
		// One pass claims only as much of the backlog as the workers can take (plus a small buffer).
		// Claiming all of it filled the run queue, blocked a goroutine per overflow and made the
		// watchdog release claims nobody had started; the pump runs again as soon as a run ends.
		window := a.distillWindow()
		claimed := 0
		for hash, rec := range records {
			if len(known) > 0 && !known[hash] {
				continue
			}
			if a.stepComplete(hash, rec, step) {
				continue
			}
			switch step {
			case entities.StepCapture:
				a.enqueueCapture(hash)
			case entities.StepOutline:
				// The barrier was checked above, for the whole pass: re-asking per source would turn
				// one pass into one vault scan per source.
				a.queueTree(hash)
			case entities.StepDistill:
				if claimed >= window {
					continue
				}
				if a.autoDistill(hash) {
					claimed++
				}
			}
		}
		return
	}
	// Only now, with every claim in a cluster, may step 4 run.
	a.requestClusterIfDue(reason)
}

// enqueueCapture schedules step 1 for a source whose bytes are not readable. It never queues the
// same source twice (the add path and the start-up sweep both look at "is there a rendering?").
func (a *App) enqueueCapture(hash string) {
	if !a.claimCapture(hash) {
		return
	}
	a.markWaiting(entities.StepCapture, hash)
	select {
	case a.captureQueue <- hash:
	default:
		// The queue is full. Do NOT drop the source (that left it unprocessed until a restart):
		// keep the claim and hand it over as soon as the worker has room.
		a.log.Warn("capture queue full — handing the source over in the background", "hash", hash)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case a.captureQueue <- hash:
					return
				case <-a.quit:
					a.releaseCapture(hash)
					a.clearWaiting(entities.StepCapture, hash)
					return
				case <-ticker.C:
				}
			}
		}()
	}
}

func (a *App) claimCapture(hash string) bool {
	a.captureMu.Lock()
	defer a.captureMu.Unlock()
	if a.capturesInflight[hash] {
		return false
	}
	a.capturesInflight[hash] = true
	return true
}

func (a *App) releaseCapture(hash string) {
	a.captureMu.Lock()
	delete(a.capturesInflight, hash)
	a.captureMu.Unlock()
}

// captureWorker runs step 1. A capture that succeeded hands over to the outline — never both at
// once for one source, and never the outline first.
func (a *App) captureWorker() {
	for {
		select {
		case <-a.quit:
			return
		case hash := <-a.captureQueue:
			a.clearWaiting(entities.StepCapture, hash)
			// The budget starts INSIDE the gate: a capture that waits for a long cluster pass must not
			// spend its OCR time on waiting.
			release := a.enterDocStep(entities.StepCapture, hash)
			ctx, cancel := context.WithTimeout(context.Background(), a.captureTimeout())
			var res usecases.CaptureResult
			res, err := a.capture.Execute(ctx, hash)
			cancel()
			release()
			a.releaseCapture(hash)
			a.publish("source", map[string]any{"hash": hash})
			if err != nil {
				a.log.Warn("capture failed", "hash", hash, "err", err)
				continue
			}
			a.log.Info("source captured", "hash", hash, "parts", res.Parts,
				"pages", res.Pages, "ocr", res.OCR, "chars", res.Chars, "rendered", res.Rendered)
			// Step 1 is done for this source; the global pump decides whether the outline stage may
			// now begin for EVERY source that owes it.
			a.pumpPipeline("capture finished")
		}
	}
}

// captureTimeout bounds one capture: OCR of a long scan is slow, but "slow" must not be "forever".
func (a *App) captureTimeout() time.Duration {
	if d, err := time.ParseDuration(a.cfg.OCR.Timeout); err == nil && d > 0 {
		// Extraction may OCR many pages, so the budget is generous but finite.
		return 10 * d
	}
	return 30 * time.Minute
}

// onRemoved reacts to a deleted source: the inbox and the association layer both changed.
func (a *App) onRemoved(hash string) {
	a.hub.Publish("source", map[string]string{"hash": hash})
	a.hub.Publish("clusters", map[string]string{"hash": hash})
	// Removing a source can be what lets the remaining sources cross a step barrier.
	a.pumpPipeline("source removed")
}

// publish emits an SSE event (no-op when nobody listens, cheap either way). It is also a no-op
// before the hub exists (a bare App in a test), so callers never have to check.
func (a *App) publish(typ string, data any) {
	if a.hub == nil {
		return
	}
	a.hub.Publish(typ, data)
}

// Run listens, starts the watcher and either blocks on the tray or on a signal.
func (a *App) Run() error {
	// The heap ceiling is set before ANY work runs — the start-up repair is one of the passes that
	// can spike, and a limit installed afterwards would have missed exactly the run it is for.
	if v := a.cfg.App.MemoryLimitMB; v > 0 {
		debug.SetMemoryLimit(int64(v) << 20)
		a.log.Info("memory limit set", "mb", v)
	}
	// Consistency is a precondition of serving, not a background chore: a stale page is a lie the
	// reader cannot distinguish from a real one.
	a.startupRepair()
	ln, err := net.Listen("tcp", a.cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.cfg.Server.Addr, err)
	}
	a.addr = ln.Addr().String()
	if err := writeEndpoint(a.addr, os.Getpid()); err != nil {
		a.log.Warn("could not write endpoint file", "err", err)
	}

	a.srv = &http.Server{Handler: a.handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		if err := a.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// The inbox watcher is the only part gated by pipeline.auto; the capture, outline and distill
	// workers always run because work is scheduled explicitly (watcher, POST /add,
	// start-up sweep).
	if a.cfg.Pipeline.Auto {
		go a.watch()
	}
	go a.captureWorker()
	go a.treeWorker()
	// Document runs across DIFFERENT sources are independent; only the same source is serialised
	// (claimDistill). One source stuck against a slow endpoint must not block every other document.
	for i := 0; i < a.cfg.Pipeline.ParallelRuns; i++ {
		go a.runWorker(a.distillQueue)
	}
	go a.runWorker(a.queryQueue)
	go a.runWorker(a.lintQueue)
	go a.runWorker(a.tagQueue)
	go a.runWorker(a.summaryQueue)
	go a.runWorker(a.vocabularyQueue)
	// Step 4 has ONE worker: the cluster step is considered over the whole vault, so two of them would
	// be two answers to one question. It takes the gate, so it never runs beside a document step.
	go a.clusterWorker()
	go a.pumpWorker()
	go a.urlWorker()
	go a.urlWorker()
	go a.urlWorker()
	go a.refreshWorker()
	go a.watchClaims()
	go a.reconcileWorker()
	go a.startupSweep()
	go a.memoryWatch()

	a.log.Info("run concurrency",
		"distill_runs", a.cfg.Pipeline.ParallelRuns,
		"parts_per_run", a.cfg.Pipeline.PartsPerRun,
		"parallel_parts", a.cfg.Pipeline.ParallelParts)
	a.log.Info("thought-keeper running",
		"version", version.String(),
		"addr", a.addr,
		"vault", a.cfg.VaultRoot(),
		"fts5", a.fts5, "pdf", a.pdfOK, "ocr", a.ocrOK,
		"agent", a.agentAvailable, "agent_model", a.cfg.OpenAI.ModelCompile, "agent_base_url", a.cfg.OpenAI.BaseURL,
		"headless", a.headless,
	)
	if !a.agentAvailable {
		a.log.Warn("agent disabled: set OPENAI_API_KEY (env or .env) to enable the document steps")
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
			a.requestQuit()
		case <-a.quit:
		}
	}()

	a.log.Info("UI", "url", a.uiURL())

	if a.headless {
		select {
		case <-a.quit:
			a.shutdown()
			return nil
		case err := <-errCh:
			a.shutdown()
			return err
		}
	}

	// The shell shows the UI and owns the lifecycle: the default build blocks on the tray and hands
	// the address to the OS (the installed PWA, otherwise the browser).
	a.runShell()
	a.shutdown()
	return nil
}

// uiURL is the address of this instance's UI. What both shells do with it is the only difference
// between them: the browser opens it, the native window loads it.
func (a *App) uiURL() string { return "http://" + a.Addr() }

// Status builds the doctor report.
func (a *App) Status() payloads.Status {
	search := "fts5"
	if !a.fts5 {
		search = "degraded"
	}
	return payloads.Status{
		Version: version.String(),
		App: payloads.AppStatus{
			Addr:      a.Addr(),
			PID:       os.Getpid(),
			Headless:  a.headless,
			StartedAt: a.startedAt.UTC().Format(time.RFC3339),
			Uptime:    time.Since(a.startedAt).Round(time.Second).String(),
			Inbox:     a.inboxWatchState(),
		},
		Vault: payloads.VaultInfo{
			Root:     a.cfg.VaultRoot(),
			Sources:  a.cfg.SourcesDir(),
			Inbox:    a.cfg.InboxDir(),
			Atoms:    a.cfg.AtomsDir(),
			Clusters: a.cfg.ClustersDir(),
			Writable: a.vaultWritable,
		},
		Index: payloads.IndexInfo{
			Path:        a.cfg.IndexDB(),
			Open:        a.db != nil,
			Outdated:    a.schemaOutdated,
			FTS5:        a.fts5,
			Search:      search,
			Sources:     persistence.Count(a.db, "sources"),
			Bookmarks:   persistence.CountWhere(a.db, "sources", "bookmark = 1"),
			Failed:      persistence.CountFailedSources(a.db),
			Findings:    persistence.CountWhere(a.db, "lint_findings", "severity <> 'info'"),
			Atoms:       persistence.Count(a.db, "atoms"),
			Clusters:    persistence.Count(a.db, "clusters"),
			Unclustered: persistence.CountUnclustered(a.db),
			Runs:        persistence.Count(a.db, "runs"),
		},
		Pipeline: a.PipelineStatus(),
		Summary:  a.summaryStatus(),
		Runs: payloads.RunsInfo{
			PipelineQueued:  len(a.distillQueue),
			PipelineRunning: a.runningDistills(),
			PipelineWorkers: a.cfg.Pipeline.ParallelRuns,
			QueryQueued:     len(a.queryQueue),
			LintQueued:      len(a.lintQueue),
			LintRunning:     a.lintRunning,
		},
		Tools: payloads.ToolsInfo{
			PDF:        a.pdfOK,
			OCR:        a.ocrOK,
			TextTool:   a.cfg.PDF.TextTool,
			Rasterizer: a.cfg.OCR.Rasterizer,
			Tesseract:  a.cfg.OCR.Tesseract,
		},
		Agent: payloads.AgentInfo{
			Configured: a.agentAvailable,
			Model:      a.cfg.OpenAI.ModelCompile,
			BaseURL:    a.cfg.OpenAI.BaseURL,
		},
		Memory: a.memoryReport(),
	}
}

// memoryWatch samples the live heap on a fixed cadence and keeps the watermark. The association
// pass takes tens of seconds; the gap is far smaller than any spike that matters, so nothing that
// costs hides. ReadMemStats is a sub-millisecond stop-the-world — at this cadence a rounding error.
func (a *App) memoryWatch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			a.observeHeap()
		}
	}
}

// observeHeap folds one reading into the watermark and returns it. When the watermark MOVES, the
// reading is stamped with the work that was running: the sampler answers "781 MB — of what, doing
// what, when".
func (a *App) observeHeap() runtime.MemStats {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rose := false
	for {
		peak := a.memPeak.Load()
		if ms.HeapAlloc <= peak {
			break
		}
		if a.memPeak.CompareAndSwap(peak, ms.HeapAlloc) {
			rose = true
			break
		}
	}
	if rose {
		work := a.gateHolder()
		if work == "" {
			work = "no run active (list reads)"
		}
		a.memPeakMu.Lock()
		a.memPeakWork, a.memPeakAt = work, time.Now().UTC()
		loggable := ms.HeapAlloc >= a.memLogPeak+64<<20 || work != a.memLogWork
		if loggable {
			a.memLogPeak, a.memLogWork = ms.HeapAlloc, work
		}
		a.memPeakMu.Unlock()
		if loggable && a.log != nil {
			a.log.Info("heap peak", "heap_mb", ms.HeapAlloc>>20, "during", work)
		}
	}
	return ms
}

// memoryReport is the heap account: the live reading now and the highest value ever observed, with
// the work that caused it. The gap between the two numbers is the story "over 900 MB, but not
// continuously" — and the label says who.
func (a *App) memoryReport() payloads.Memory {
	ms := a.observeHeap()
	const mb = 1 << 20
	a.memPeakMu.Lock()
	work, at := a.memPeakWork, a.memPeakAt
	a.memPeakMu.Unlock()
	m := payloads.Memory{
		HeapMB:     int64(ms.HeapAlloc / mb),
		PeakHeapMB: int64(a.memPeak.Load() / mb),
		SysMB:      int64(ms.Sys / mb),
		NumGC:      ms.NumGC,
		PeakWork:   work,
	}
	if !at.IsZero() {
		m.PeakAt = at.Format(time.RFC3339)
	}
	return m
}

// Addr returns the address the app is (or will be) listening on.
func (a *App) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

func (a *App) toggleWatcher() {
	a.mu.Lock()
	a.watcherPaused = !a.watcherPaused
	paused := a.watcherPaused
	a.mu.Unlock()
	a.log.Info("watcher toggled", "paused", paused)
}

func (a *App) requestQuit() {
	a.quitOnce.Do(func() { close(a.quit) })
}

func (a *App) shutdown() {
	a.closeOnce.Do(func() {
		a.log.Info("shutting down")
		if a.srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.srv.Shutdown(ctx)
		}
		if a.db != nil {
			_ = a.db.Close()
		}
		if a.lock != nil {
			_ = a.lock.Unlock()
		}
		removeEndpoint()
	})
}

// watch intakes files dropped into the inbox. Phase 2 hashes and indexes them;
// distilling into atoms and pages follows in later phases.
func (a *App) watch() {
	dir := a.cfg.InboxDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.log.Warn("cannot create inbox", "dir", dir, "err", err)
		return
	}
	w, err := a.addInboxWatch(dir)
	if err != nil {
		// The drop zone must keep working: inotify is the fast path, not the only one. While the budget
		// is exhausted the inbox is POLLED, and the watch is retried so the app returns to inotify as
		// soon as another program releases its watches.
		a.reportInboxWatch(err)
		a.log.Warn("inbox watch unavailable — polling the drop zone instead",
			"dir", dir, "err", err, "hint", inboxWatchHint(err))
		a.pollInbox(dir)
		return
	}
	a.reportInboxWatch(nil)
	a.log.Info("watching inbox", "dir", dir)
	a.watchInboxEvents(w)
}

// addInboxWatch takes the inotify watch on the inbox. A failure here is most often ENOSPC, which does
// NOT mean a full disk: the kernel's per-user inotify budget (fs.inotify.max_user_watches) is used up by
// other programs, and it comes back when they close.
func (a *App) addInboxWatch(dir string) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

// watchInboxEvents is the inotify path. It returns when the watch is gone or the app quits.
func (a *App) watchInboxEvents(w *fsnotify.Watcher) {
	defer func() { _ = w.Close() }()
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			a.mu.Lock()
			paused := a.watcherPaused
			a.mu.Unlock()
			if paused || !ev.Has(fsnotify.Create) {
				continue
			}
			a.handleDrop(ev.Name)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			a.log.Warn("watcher error", "err", err)
		case <-a.quit:
			return
		}
	}
}

// pollInbox is the fallback drop zone: it scans the inbox for settled files and keeps trying the watch.
func (a *App) pollInbox(dir string) {
	const (
		scanEvery  = 5 * time.Second
		retryEvery = 30 * time.Second
	)
	scan := time.NewTicker(scanEvery)
	defer scan.Stop()
	retry := time.NewTicker(retryEvery)
	defer retry.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-retry.C:
			w, err := a.addInboxWatch(dir)
			if err != nil {
				a.log.Debug("inbox watch still unavailable", "err", err)
				continue
			}
			a.reportInboxWatch(nil)
			a.log.Info("inbox watch recovered — watching instead of polling", "dir", dir)
			a.watchInboxEvents(w)
			return
		case <-scan.C:
			a.scanInbox(dir)
		}
	}
}

// scanInbox hands every SETTLED file of the inbox to the intake. A file still being written is left
// alone: it is picked up on a later scan, and `handleDrop` waits its own debounce again.
func (a *App) scanInbox(dir string) {
	delay, err := time.ParseDuration(a.cfg.Pipeline.Debounce)
	if err != nil || delay <= 0 {
		delay = 500 * time.Millisecond
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < delay {
			continue
		}
		a.handleDrop(filepath.Join(dir, entry.Name()))
	}
}

// reportInboxWatch records whether the inbox is watched (nil error) or only polled, so the status
// report can say it instead of leaving a silent dead drop zone.
func (a *App) reportInboxWatch(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inboxWatched = err == nil
	a.inboxError = ""
	if err != nil {
		a.inboxError = err.Error()
	}
}

// inboxWatchState reads the drop zone's state for the status report.
func (a *App) inboxWatchState() payloads.InboxWatch {
	a.mu.Lock()
	defer a.mu.Unlock()
	return payloads.InboxWatch{Watched: a.inboxWatched, Error: a.inboxError}
}

// inboxWatchHint explains the failure a reader cannot otherwise understand: ENOSPC from inotify is not
// a full disk.
func inboxWatchHint(err error) string {
	if errors.Is(err, syscall.ENOSPC) {
		return "the kernel's inotify budget is exhausted (fs.inotify.max_user_watches) — this is NOT the disk; raise the limit and the app returns to watching"
	}
	return ""
}

// handleDrop intakes a dropped file after a short settle delay, then removes it
// from the inbox.
func (a *App) handleDrop(path string) {
	if !a.claimDrop(path) {
		return
	}
	go func() {
		defer a.releaseDrop(path)
		delay, err := time.ParseDuration(a.cfg.Pipeline.Debounce)
		if err != nil || delay <= 0 {
			delay = 500 * time.Millisecond
		}
		time.Sleep(delay)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return
		}
		f, err := os.Open(path)
		if err != nil {
			a.log.Warn("cannot open dropped file", "path", path, "err", err)
			return
		}
		// Intake writes a source object and its row: shared gate. It may run beside a document step;
		// the cluster step waits for it.
		a.gate.RLock()
		src, err := a.intake.Execute(context.Background(), usecases.IntakeInput{Name: filepath.Base(path), Reader: f})
		a.gate.RUnlock()
		_ = f.Close()
		if err != nil {
			a.log.Warn("intake failed", "path", path, "err", err)
			return
		}
		if err := os.Remove(path); err != nil {
			a.log.Warn("could not remove dropped file", "path", path, "err", err)
		}
		a.log.Info("source added", "hash", src.Hash, "kind", src.Kind, "bytes", src.Bytes, "file", filepath.Base(path))
		a.enqueuePipeline(src.Hash)
	}()
}

// claimDrop makes sure ONE intake runs per dropped file. A watcher reports a create more than once, and
// the polling fallback can meet a file a watch event already took.
func (a *App) claimDrop(path string) bool {
	a.dropMu.Lock()
	defer a.dropMu.Unlock()
	if a.dropsInflight == nil {
		a.dropsInflight = map[string]bool{}
	}
	if a.dropsInflight[path] {
		return false
	}
	a.dropsInflight[path] = true
	return true
}

func (a *App) releaseDrop(path string) {
	a.dropMu.Lock()
	delete(a.dropsInflight, path)
	a.dropMu.Unlock()
}

// enqueueTree schedules a tree build off the request path (non-blocking). A source is never
// queued twice: the add path and the start-up sweep can both see "no tree yet", and building
// the same document twice is wasted work that also leaves the row flapping between states.
func (a *App) enqueueTree(hash string) {
	if !a.stepBarrierAllows(entities.StepOutline) {
		a.log.Debug("outline waits for every source to finish capture", "hash", hash)
		return
	}
	a.queueTree(hash)
}

// queueTree hands one source to the outline worker. It does NOT re-check the global barrier: the pump
// established it once for the whole pass, and asking again per source made one pass cost a full vault
// scan per queued outline (the exact quadratic a vault of a few hundred sources feels).
func (a *App) queueTree(hash string) {
	if !a.claimTree(hash) {
		return
	}
	a.markWaiting(entities.StepOutline, hash)
	select {
	case a.treeQueue <- hash:
	default:
		// A full queue is backpressure, not a reason to forget the source. Keep the claim and hand
		// it over when the worker has room; the claim also keeps the sweep from queueing it twice.
		a.log.Warn("tree queue full — handing the source over in the background", "hash", hash)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case a.treeQueue <- hash:
					return
				case <-a.quit:
					a.releaseTree(hash)
					a.clearWaiting(entities.StepOutline, hash)
					return
				case <-ticker.C:
				}
			}
		}()
	}
}

// claimTree marks a source as queued for a tree build; false when one is already pending.
func (a *App) claimTree(hash string) bool {
	a.treeMu.Lock()
	defer a.treeMu.Unlock()
	if a.treesInflight[hash] {
		return false
	}
	a.treesInflight[hash] = true
	return true
}

func (a *App) releaseTree(hash string) {
	a.treeMu.Lock()
	delete(a.treesInflight, hash)
	a.treeMu.Unlock()
}

// treeWorker builds trees for intaken sources.
func (a *App) treeWorker() {
	for {
		select {
		case <-a.quit:
			return
		case hash := <-a.treeQueue:
			a.clearWaiting(entities.StepOutline, hash)
			if !a.stepBarrierAllows(entities.StepOutline) {
				// A source was added while this job waited: the capture barrier is no longer met.
				// Release the claim; the pump that follows the new capture queues this source again.
				a.releaseTree(hash)
				a.log.Info("outline deferred until every source has finished capture", "hash", hash)
				continue
			}
			// The outline is a DOCUMENT step like the others: it takes the shared side of the gate, so
			// the cluster step cannot write pages while a tree is being built. It used to run outside the
			// gate, which is exactly the overlap the four steps forbid.
			release := a.enterDocStep(entities.StepOutline, hash)
			var res usecases.TreeResult
			res, err := a.buildTree.Execute(context.Background(), hash)
			release()
			a.releaseTree(hash)
			if err != nil {
				a.log.Warn("tree build failed", "hash", hash, "err", err)
				continue
			}
			a.log.Info("tree built", "hash", hash, "nodes", res.Nodes, "pages", res.Pages, "ocr", res.OCR, "reopened", res.Reopened)
			// The outline is complete for this source; the pump decides whether the distill stage is
			// now open for all sources (a reopened part is simply part of that stage).
			a.pumpPipeline("outline finished")
		}
	}
}

// runProgress is what one run of a source achieved.
type runProgress struct {
	atoms int
	// left is how much work the run left behind: the open parts for a document run, the uncited
	// claims for a cluster pass. Less of it than before is progress.
	left int
}

// urlJob is one URL the vault owes a fetch. Origin and WikiPage make a wiki page's link recoverable:
// a failed fetch is reported against the page that declared it, and the source row it claimed stays
// listed — pending, then red with the reason.
type urlJob struct {
	URL        string
	Origin     string
	WikiPage   string
	ParentHash string
}

// runJob is one piece of work for a run worker.
type runJob struct {
	runID ulid.ULID
	op    entities.RunOp
	// step is which pipeline step this run executes (distill for a document run, compile for the
	// cluster step): the four steps are steered individually, so a run says which one it is.
	step     entities.Step
	hash     string
	question string
	url      string
	// page is the wiki page a tagging run works on.
	page ulid.ULID
	// namespace is which name vocabulary a curation run works on (claim topics/claim topics).
	namespace entities.VocabularyNamespace
	// rebranch says the run re-derives the TOPIC labels of the claims instead of curating a vocabulary.
	rebranch bool
	// reset replaces the derivation instead of continuing it. Continuing is the default: a
	// book needs several runs, and each one must keep the progress of the previous ones.
	reset bool
}

// ScheduleDistill creates a DOCUMENT run for one source and enqueues it, returning the run id for the 202
// response. It CONTINUES an unfinished derivation (a long document needs several runs); `reset`
// throws the previous one away and starts over (capture, outline and claims included). A source is
// never derived twice concurrently, and the CLUSTER step is not part of this run: it runs over the
// whole vault and only while no document step runs.
func (a *App) ScheduleDistill(hash string, reset bool) (string, error) {
	if !a.agentAvailable {
		return "", agentNotConfigured()
	}
	if !reset && !a.stepBarrierAllows(entities.StepDistill) {
		return "", types.New("conflict", "distill waits until every source has finished the outline step", types.ErrConflict)
	}
	ctx := context.Background()
	src, err := a.getSource.Execute(ctx, hash)
	if err != nil {
		return "", err
	}
	if src.RSS || src.Sitemap {
		return "", types.New("conflict",
			"a feed or a sitemap is a list of sources; it has no document to derive — refresh it instead",
			types.ErrConflict)
	}
	if !a.claimDistill(hash) {
		return "", types.New("conflict", "a run for this source is already queued or running", types.ErrConflict)
	}
	runID, err := a.startRun.Execute(ctx, entities.RunPipeline, a.cfg.OpenAI.ModelCompile, entities.StepDistill, hash)
	if err != nil {
		a.releaseDistill(hash)
		return "", err
	}
	job := runJob{runID: runID, op: entities.RunPipeline, hash: hash, reset: reset}
	// A pressed run goes to the FRONT: it must not wait behind the automatic runs of other
	// documents (observed: a reset-follow-up queued behind sixty auto-continue runs).
	select {
	case a.distillFirst <- job:
		a.markQueued(hash, 0)
	case a.distillQueue <- job:
		a.markQueued(hash, len(a.distillQueue))
	default:
		a.releaseDistill(hash)
		return "", types.New("conflict",
			fmt.Sprintf("run queue is full (%d waiting) — nothing was started", len(a.distillQueue)+len(a.distillFirst)),
			types.ErrConflict)
	}
	a.clearStalled(hash)
	// The run is claimed and queued: tell the UI now. Runs are serialized, so the worker may not
	// pick this up for a while, and the row must show that a step is running.
	// from the moment the button was pressed, not from the moment the run starts.
	a.publish("source", map[string]string{"hash": hash})
	return runID.String(), nil
}

// ScheduleSummary writes one source's overview. It is the run behind the Summary tab's Generate
// button, the post-distill refresh and the consistency backfill. The summary is derived from the
// claims, so the run refuses until the source's distill step is complete.
func (a *App) ScheduleSummary(hash string) (string, error) {
	if !a.agentAvailable {
		return "", agentNotConfigured()
	}
	ctx := context.Background()
	if _, err := a.getSource.Execute(ctx, hash); err != nil {
		return "", err
	}
	if !a.claimSummary(hash) {
		return "", types.New("conflict", "a summary run for this source is already queued or running", types.ErrConflict)
	}
	runID, err := a.startRun.Execute(ctx, entities.RunSummary, a.cfg.OpenAI.ModelCompile, "", hash)
	if err != nil {
		a.releaseSummary(hash)
		return "", err
	}
	a.markQueuedRun(runID, "summary")
	select {
	case a.summaryQueue <- runJob{runID: runID, op: entities.RunSummary, hash: hash}:
	default:
		a.releaseSummary(hash)
		_ = a.startRun.Abandon(ctx, runID, "the summary queue was full")
		a.clearRun(runID)
		return "", types.New("conflict", "the summary queue is full", types.ErrConflict)
	}
	return runID.String(), nil
}

// requestSummary asks for a summary and ignores "already queued": the caller has no interest in the
// difference between "scheduled now" and "scheduled a moment ago".
func (a *App) requestSummary(hash string) {
	if !a.agentAvailable || hash == "" {
		return
	}
	if _, err := a.ScheduleSummary(hash); err != nil && !errors.Is(err, types.ErrConflict) {
		a.log.Warn("summary: could not schedule", "hash", hash, "err", err)
	}
}

// summaryBackfillPerPass bounds how many missing summaries the consistency job queues in one tick. A
// vault from before the summary existed owes one per source; writing them all at once would be
// hundreds of model calls in a row. Two per pass lets the backlog drain in the background while the
// Summary tab's Generate button serves any single source immediately.
const summaryBackfillPerPass = 2

// requestSummaries queues the next few missing summaries.
func (a *App) requestSummaries(missing []string) {
	if !a.agentAvailable || len(missing) == 0 {
		return
	}
	queued := 0
	for _, hash := range missing {
		if queued >= summaryBackfillPerPass {
			break
		}
		if _, err := a.ScheduleSummary(hash); err == nil {
			queued++
		}
	}
	if queued > 0 {
		a.log.Info("consistency: summaries queued", "queued", queued, "missing", len(missing))
	}
}

// claimSummary marks a source as having a summary run queued or in flight.
func (a *App) claimSummary(hash string) bool {
	a.summaryMu.Lock()
	defer a.summaryMu.Unlock()
	if a.summaryActive[hash] {
		return false
	}
	a.summaryActive[hash] = true
	return true
}

func (a *App) releaseSummary(hash string) {
	a.summaryMu.Lock()
	delete(a.summaryActive, hash)
	a.summaryMu.Unlock()
}

// markSummaryRunning records that the worker owns this summary right now (waiting for the gate
// included): the Status lens shows it beside the queued ones.
func (a *App) markSummaryRunning(hash string, running bool) {
	a.summaryMu.Lock()
	if running {
		a.summaryRunning[hash] = true
	} else {
		delete(a.summaryRunning, hash)
	}
	a.summaryMu.Unlock()
}

// resetSummaryMissing starts a new consistency walk: the backlog is measured from scratch, so a
// summary whose file appeared since the last walk is not reported as missing.
func (a *App) resetSummaryMissing() {
	a.summaryMu.Lock()
	a.summaryRemaining = make(map[string]bool)
	a.summaryRemainingAt = time.Now()
	a.summaryMu.Unlock()
}

// mergeSummaryMissing adds one consistency slice's missing summaries to the backlog. The walk is
// sliced; merging keeps the whole walk's picture instead of reporting only the last batch.
func (a *App) mergeSummaryMissing(hashes []string) {
	a.summaryMu.Lock()
	for _, hash := range hashes {
		if hash != "" {
			a.summaryRemaining[hash] = true
		}
	}
	a.summaryRemainingAt = time.Now()
	a.summaryMu.Unlock()
}

// completeSummary drops a source from the backlog and the running set: the file exists now.
func (a *App) completeSummary(hash string) {
	a.summaryMu.Lock()
	delete(a.summaryRemaining, hash)
	delete(a.summaryRunning, hash)
	a.summaryMu.Unlock()
}

// summaryStatus is the backfill's snapshot for GET /api/v1/status; nil when there is nothing to do.
func (a *App) summaryStatus() *payloads.SummaryStatus {
	a.summaryMu.Lock()
	missing := len(a.summaryRemaining)
	running := len(a.summaryRunning)
	at := a.summaryRemainingAt
	a.summaryMu.Unlock()
	queued := len(a.summaryQueue)
	if missing == 0 && queued == 0 && running == 0 {
		return nil
	}
	out := &payloads.SummaryStatus{Missing: missing, Queued: queued, Running: running}
	if !at.IsZero() {
		out.At = at.UTC().Format(time.RFC3339)
	}
	return out
}

// sourceTagBackfillPerPass bounds how many never-tagged sources the consistency job queues per tick:
// deriving labels is a model call, and a vault from before the feature owes one per source.
const sourceTagBackfillPerPass = 2

// ScheduleSourceTag derives one source's auto tags. It shares the tag worker with wiki pages; the run
// row is finished by the use case.
func (a *App) ScheduleSourceTag(hash string) (string, error) {
	if !a.agentAvailable {
		return "", agentNotConfigured()
	}
	ctx := context.Background()
	if _, err := a.getSource.Execute(ctx, hash); err != nil {
		return "", err
	}
	if !a.claimSourceTag(hash) {
		return "", types.New("conflict", "a tag run for this source is already queued or running", types.ErrConflict)
	}
	runID, err := a.startRun.Execute(ctx, entities.RunTag, a.cfg.OpenAI.ModelCompile, "", hash)
	if err != nil {
		a.releaseSourceTag(hash)
		return "", err
	}
	a.markQueuedRun(runID, "source tags")
	select {
	case a.tagQueue <- runJob{runID: runID, op: entities.RunTag, hash: hash}:
	default:
		a.releaseSourceTag(hash)
		_ = a.startRun.Abandon(ctx, runID, "the tag queue was full")
		a.clearRun(runID)
		return "", types.New("conflict", "the tag queue is full", types.ErrConflict)
	}
	return runID.String(), nil
}

// requestSourceTag asks for the derivation and ignores "already queued".
func (a *App) requestSourceTag(hash string) {
	if !a.agentAvailable || hash == "" {
		return
	}
	if _, err := a.ScheduleSourceTag(hash); err != nil && !errors.Is(err, types.ErrConflict) {
		a.log.Warn("source tags: could not schedule", "hash", hash, "err", err)
	}
}

// requestSourceTags queues the next few never-tagged sources of a consistency pass.
func (a *App) requestSourceTags(missing []string) {
	if !a.agentAvailable || len(missing) == 0 {
		return
	}
	queued := 0
	for _, hash := range missing {
		if queued >= sourceTagBackfillPerPass {
			break
		}
		if _, err := a.ScheduleSourceTag(hash); err == nil {
			queued++
		}
	}
	if queued > 0 {
		a.log.Info("consistency: source tags queued", "queued", queued, "missing", len(missing))
	}
}

func (a *App) claimSourceTag(hash string) bool {
	a.sourceTagMu.Lock()
	defer a.sourceTagMu.Unlock()
	if a.sourceTagActive[hash] {
		return false
	}
	a.sourceTagActive[hash] = true
	return true
}

func (a *App) releaseSourceTag(hash string) {
	a.sourceTagMu.Lock()
	delete(a.sourceTagActive, hash)
	a.sourceTagMu.Unlock()
}

// ScheduleQuery creates a query run and enqueues it.
func (a *App) ScheduleQuery(question string) (string, error) {
	if !a.agentAvailable {
		return "", agentNotConfigured()
	}
	runID, err := a.startRun.ExecuteQuery(context.Background(), a.cfg.OpenAI.ModelReason, question)
	if err != nil {
		return "", err
	}
	select {
	case a.queryQueue <- runJob{runID: runID, op: entities.RunQuery, question: question}:
	default:
		return "", types.New("conflict", "run queue is full", types.ErrConflict)
	}
	return runID.String(), nil
}

// ScheduleLint creates a lint run and enqueues it.
func (a *App) ScheduleLint(scope string) (string, error) {
	runID, err := a.startRun.Execute(context.Background(), entities.RunLint, a.cfg.OpenAI.ModelReason, entities.StepCluster, scope)
	if err != nil {
		return "", err
	}
	select {
	case a.lintQueue <- runJob{runID: runID, op: entities.RunLint}:
		a.markQueuedRun(runID, "lint")
	default:
		return "", types.New("conflict", "run queue is full", types.ErrConflict)
	}
	return runID.String(), nil
}

// maintJob is one background job that is not a pipeline step.
type maintJob struct {
	label  string
	target string
	state  string
	detail string
	since  time.Time
}

// MarkMaintenance registers a background job and returns the function that clears it. It is for the
// passes that run SYNCHRONOUSLY behind a request (reindex, reconcile, cleanup, rebuild-trees, a URL
// refresh): they can take a while, and "the button did nothing for two minutes" is exactly what the
// Running list exists to prevent.
func (a *App) MarkMaintenance(label, target string) func() {
	_, done := a.beginMaintenance(label, target)
	return done
}

// beginMaintenance registers a maintenance job and keeps its id, so a pass that WAITS for the gate can
// say so instead of claiming to run.
func (a *App) beginMaintenance(label, target string) (ulid.ULID, func()) {
	id := ulid.Make()
	a.maintMu.Lock()
	a.maint[id] = maintJob{label: label, target: target, state: "running", since: time.Now().UTC()}
	a.maintMu.Unlock()
	// The Running list is a STATE event, so the UI has to be told: without this a pass that runs for two
	// minutes looked like a button that did nothing.
	a.publishPipeline()
	return id, func() {
		a.maintMu.Lock()
		delete(a.maint, id)
		a.maintMu.Unlock()
		a.publishPipeline()
	}
}

// ExclusiveWrite runs a maintenance pass that writes the vault inside the exclusive gate: it waits for
// the document steps and the cluster pass like any other vault write, and it is listed as a JOB while
// it waits.
func (a *App) ExclusiveWrite(ctx context.Context, label string, fn func(context.Context) error) error {
	id, done := a.beginMaintenance(label, "")
	defer done()
	a.markMaintWaiting(id, a.gateHolderReason("this pass rewrites the vault"))
	a.log.Info("maintenance: waiting for the gate", "job", label)
	return a.clusterStep(ctx, 0, func(ctx context.Context) error {
		a.markRunRunning(id)
		a.log.Info("maintenance: gate acquired", "job", label)
		return fn(ctx)
	})
}

// sharedWrite runs a maintenance WRITE that must not overlap the cluster pass but may run beside
// document steps: tagging writes a page's frontmatter, never a claim or a document's structure.
func (a *App) sharedWrite(ctx context.Context, fn func(context.Context) error) error {
	a.gate.RLock()
	defer a.gate.RUnlock()
	return fn(ctx)
}

// markQueuedRun records a queued maintenance RUN (one of the ops that has a worker). runOne turns it
// into "running" when a worker takes it and clears it when the run ends.
func (a *App) markQueuedRun(runID ulid.ULID, label string) {
	a.maintMu.Lock()
	a.maint[runID] = maintJob{label: label, state: "queued", since: time.Now().UTC()}
	a.maintMu.Unlock()
	a.publishPipeline()
}

// gateHolderReason says WHAT a job waits for. "the document steps" was a guess: the cluster pass, the
// consistency job and a curation all hold the same gate, and a reader who is told the wrong holder
// looks for the wrong thing.
func (a *App) gateHolderReason(why string) string {
	if holder := a.gateHolder(); holder != "" {
		return "waiting for " + holder + ": " + why
	}
	return "waiting for the vault gate: " + why
}

// gateHolder names what holds the vault's gate right now, or "" when nobody does.
func (a *App) gateHolder() string {
	a.pipeMu.Lock()
	cluster := a.pipe.clusterRunning
	docs := 0
	for step, n := range a.pipe.docRunning {
		if step.IsDocument() {
			docs += n
		}
	}
	a.pipeMu.Unlock()
	if cluster > 0 {
		return "the cluster pass (it is grouping claims)"
	}
	for _, j := range a.maintenanceJobs() {
		if j.State != "running" {
			continue
		}
		switch j.Step {
		case "vocabulary":
			return "the vocabulary curation (it rewrites claim names)"
		default:
			return "the " + j.Step + " pass (it rewrites the vault)"
		}
	}
	if docs > 0 {
		return "the running document steps"
	}
	return ""
}

// startupRunDelay is how long a start waits before it queues ANY automatic run: long enough for the
// reader to press something by hand first, short enough that a start still heals itself.
const startupRunDelay = 20 * time.Second

// vocabularyUnitTimeout bounds one curation: three namespaces, each with its own bounded model
// calls, plus the renames. It is a ceiling for a run that has gone wrong, not a schedule — and it can
// be generous because the run does NOT hold the gate while it asks: a pass that stops halfway through a
// grown catalog leaves two of three namespaces untouched, which is exactly what "no result" looked
// like. Measured: ~0.5 s of model time per name, so a catalog of a few thousand names is minutes.
const vocabularyUnitTimeout = 30 * time.Minute

// vocabularyGateWait is the allowance for reaching the gate. It is NOT part of the unit budget: a pass
// that waited ten minutes for a book to finish deriving must still have its full working time.
const vocabularyGateWait = 10 * time.Minute

// vocabularyQuestionsAtOnce is how many of a namespace's questions are in flight at the same time. A
// question writes nothing, so the only limit is the endpoint: four keeps a grown catalog to minutes
// without hammering the model with dozens of parallel calls.
const vocabularyQuestionsAtOnce = 4

// markMaintWaiting says that a maintenance run has been taken but cannot start yet, and why. It is the
// same distinction the document steps make: `running` means the work is happening.
func (a *App) markMaintWaiting(runID ulid.ULID, why string) {
	a.maintMu.Lock()
	if j, ok := a.maint[runID]; ok {
		j.state = "waiting"
		j.detail = why
		a.maint[runID] = j
	}
	a.maintMu.Unlock()
	a.publishPipeline()
}

func (a *App) markRunRunning(runID ulid.ULID) {
	a.maintMu.Lock()
	if j, ok := a.maint[runID]; ok {
		j.state = "running"
		a.maint[runID] = j
	}
	a.maintMu.Unlock()
	a.publishPipeline()
}

// setMaintDetail says what a running maintenance job is doing right now. It is what makes a pass that
// takes a while watchable: the phase, not just the fact that something is running.
func (a *App) setMaintDetail(runID ulid.ULID, detail string) {
	a.maintMu.Lock()
	if j, ok := a.maint[runID]; ok {
		j.detail = detail
		a.maint[runID] = j
	}
	a.maintMu.Unlock()
	a.publishPipeline()
	// A maintenance pass that CHANGES something (a curation writes the vocabulary, a rename moves
	// claims) has to reach the UI while it works, not only when it ends: the numbers on the Status page
	// are the reader's only view of it, and a pass that fails halfway must still show what it did.
	a.publish("vocabulary", map[string]any{"namespace": detail})
}

func (a *App) clearRun(runID ulid.ULID) {
	a.maintMu.Lock()
	_, had := a.maint[runID]
	delete(a.maint, runID)
	a.maintMu.Unlock()
	if had {
		a.publishPipeline()
	}
}

// clusterPublishEvery is the minimum gap between two live Job-Queue frames from a clustering run.
const clusterPublishEvery = 2 * time.Second

// setClusterProgress receives the live cluster-run progress from ClusterPass.
func (a *App) setClusterProgress(p usecases.ClusterProgress) {
	// The Job Queue is event-driven, but a naming run reports per batch — hundreds of frames. The
	// Status page needs the numbers to MOVE, not the client to reload /pipeline hundreds of times, so
	// publish at most every clusterPublishEvery and at once on a phase change or completion.
	a.clusterProgressMu.Lock()
	a.clusterProgress = p
	now := time.Now()
	force := p.Complete || p.Phase != a.clusterPhase
	publish := force || a.clusterPublished.IsZero() || now.Sub(a.clusterPublished) >= clusterPublishEvery
	if publish {
		a.clusterPublished = now
		a.clusterPhase = p.Phase
	}
	a.clusterProgressMu.Unlock()
	if publish {
		a.publishPipeline()
	}
}

func (a *App) clearClusterProgress() {
	a.setClusterProgress(usecases.ClusterProgress{})
}

// clusterJobDetail is the Job Queue line for the clustering phase. It answers how far the run is
// through its phase counts, rather than showing only "cluster" for an hour.
func (a *App) clusterJobDetail() string {
	a.clusterProgressMu.Lock()
	p := a.clusterProgress
	a.clusterProgressMu.Unlock()
	if p.Phase == "" {
		return ""
	}
	s := fmt.Sprintf("clustering · %s · %d/%d topics · %d/%d claims · %d clusters",
		p.Phase, p.TopicsDone, p.TopicsTotal, p.ClaimsDone, p.ClaimsTotal, p.Clusters)
	if p.Attached > 0 {
		s += fmt.Sprintf(" · %d attached", p.Attached)
	}
	if p.New > 0 {
		s += fmt.Sprintf(" · %d new", p.New)
	}
	if p.Remaining > 0 {
		s += fmt.Sprintf(" · %d claims open", p.Remaining)
	}
	if p.Complete {
		s += " · clustering complete"
	}
	return s
}

// maintenanceJobs is what the Running list appends: every registered background job, newest last.
func (a *App) maintenanceJobs() []payloads.Job {
	a.maintMu.Lock()
	defer a.maintMu.Unlock()
	ids := make([]ulid.ULID, 0, len(a.maint))
	for id := range a.maint {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	out := make([]payloads.Job, 0, len(ids))
	for _, id := range ids {
		j := a.maint[id]
		out = append(out, payloads.Job{
			Step: j.label, State: j.state, Target: j.target, Detail: j.detail,
			Since: j.since.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// ScheduleVocabularyCurate curates the name vocabularies: ONE unit that adopts the baseline
// and then asks the model, namespace by namespace, coarse to fine (claim topics → claim topics — the page
// is the larger context). An empty namespace is that whole unit; naming one is for a targeted retry.
// The names the agent invented at write time are corrected afterwards, never constrained up front, and
// it rewrites atoms (a claim topic) and pages (tags), so it runs inside the exclusive gate like the
// cluster step — a document step must not be writing claims while their labels move.
// vocabularyCooldown is how often the automatic curation may run. It costs minutes of model time
// and rewrites names, so it is not asked on every tick — but it IS part of the automatic flow.
const vocabularyCooldown = 20 * time.Minute

// vocabularyReportTimeout bounds the READ that decides whether a pass is due. It reads the index, not
// the model.
const vocabularyReportTimeout = 30 * time.Second

// requestVocabularyIfDue runs the name curation on its own. Names drift on every write — the
// compile invents a topic beside the one the vocabulary already settled — and a pass that only ever
// runs when a reader presses a button fixes the drift only when a reader happens to look. The pass is
// bounded twice: one at a time, and at most once per cooldown; and it is skipped entirely when the
// report says the vocabulary has nothing undecided.
func (a *App) requestVocabularyIfDue(reason string) {
	if !a.agentAvailable {
		return
	}
	a.vocabMu.Lock()
	if a.vocabActive || time.Since(a.vocabLast) < vocabularyCooldown {
		a.vocabMu.Unlock()
		return
	}
	a.vocabMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), vocabularyReportTimeout)
	defer cancel()
	report, err := a.curate.Report.Execute(ctx)
	if err != nil {
		return
	}
	fresh := 0
	for _, h := range report {
		fresh += len(h.New)
		// A flagged family that still contains an unreviewed name is work too. Once the model has
		// judged every member, the automatic pass stops asking; a manual Curate can still revisit.
		fresh += h.UnreviewedFamilies
		// Collisions are deliberately NOT counted: two spellings of one name that the model has
		// already judged are a REPORT, not a wake-up. Counting them queued a pass every cooldown
		// that could not ask a single question.
		if !h.BroadTokensLearned {
			fresh++ // the detector has not learned this vault's broad tokens yet
		}
	}
	if fresh == 0 {
		return
	}
	a.vocabMu.Lock()
	a.vocabActive = true
	a.vocabLast = time.Now()
	a.vocabMu.Unlock()
	if _, err := a.ScheduleVocabularyCurate(""); err != nil {
		a.vocabMu.Lock()
		a.vocabActive = false
		a.vocabMu.Unlock()
		a.log.Warn("vocabulary: automatic pass not queued", "err", err)
		return
	}
	a.log.Info("vocabulary: automatic pass queued", "reason", reason, "new_names", fresh)
}

// ScheduleRebranch queues the topic-label repair: the claims are re-labelled from their own text, in
// a bounded batch, and the rest follows on the next press (oldest first).
func (a *App) ScheduleVocabularyRebranch() (string, error) {
	if !a.agentAvailable {
		return "", agentNotConfigured()
	}
	// Fail BEFORE a run row exists: an unwired use case used to start a run that died in the worker with
	// "no model or no atom repository configured" — a run nobody can act on. (That is exactly what a
	// stale wiring produced once: the app held the zero value, and every press failed instantly.)
	if a.rebranch.Gateway == nil || a.rebranch.Atoms == nil {
		return "", types.New("not_implemented", "re-topic is not configured", types.ErrNotImplemented)
	}
	runID, err := a.startRun.Execute(context.Background(), entities.RunVocabulary,
		a.cfg.OpenAI.ModelCompile, entities.Step(""), "retopic")
	if err != nil {
		return "", err
	}
	select {
	case a.vocabularyQueue <- runJob{runID: runID, op: entities.RunVocabulary, rebranch: true}:
		a.markQueuedRun(runID, "retopic claims")
	default:
		return "", types.New("conflict", "run queue is full", types.ErrConflict)
	}
	return runID.String(), nil
}

func (a *App) ScheduleVocabularyCurate(ns entities.VocabularyNamespace) (string, error) {
	if !a.agentAvailable {
		return "", agentNotConfigured()
	}
	target := "vocabulary:all"
	if ns != "" {
		target = "vocabulary:" + string(ns)
	}
	runID, err := a.startRun.Execute(context.Background(), entities.RunVocabulary,
		a.cfg.OpenAI.ModelCompile, entities.StepCluster, target)
	if err != nil {
		return "", err
	}
	select {
	case a.vocabularyQueue <- runJob{runID: runID, op: entities.RunVocabulary, namespace: ns}:
		a.markQueuedRun(runID, "vocabulary")
	default:
		return "", types.New("conflict", "run queue is full", types.ErrConflict)
	}
	return runID.String(), nil
}

// ScheduleCapture re-runs step 1 for one source: it captures again (a source whose bytes are not
// readable) and hands over to the outline, so the four steps stay in order. For a source that is
// readable as it is (Markdown, text, a URL snapshot) there is nothing to extract — the useful part
// there is the title, which is derived from the object again.
func (a *App) ScheduleCapture(hash string) (bool, string, error) {
	ctx := context.Background()
	src, err := a.getSource.Execute(ctx, hash)
	if err != nil {
		return false, "", err
	}
	if src.Bookmark {
		return false, "a bookmark is collected, not derived", nil
	}
	if src.RSS || src.Sitemap {
		return false, "a feed or a sitemap is a list: refresh it instead of capturing it", nil
	}
	if !src.ToEntity().NeedsCapture() {
		return false, "the source is readable as it is — its title comes from the object (reindex restores it)", nil
	}
	a.enqueuePipeline(hash)
	return true, "", nil
}

// ReconcileNow runs the consistency job by hand (the same pass the periodic worker does) and asks
// the cluster step for a pass afterwards.
func (a *App) ReconcileNow() (usecases.ReconcileResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pass := a.reconcilePass
	pass.Offset = 0
	res, err := pass.Execute(ctx)
	if err != nil {
		return res, err
	}
	for _, hash := range res.Invalidated {
		a.log.Warn("consistency: source invalidated — deriving it again", "hash", hash, "reason", "manual")
		a.publish("source", map[string]any{"hash": hash})
	}
	if res.Languages > 0 || res.Identities > 0 || res.Ownerships > 0 {
		// A manual consistency run may have backfilled languages, identities or an owner. Re-read
		// the visible source list and detail instead of waiting for the next unrelated event.
		a.publish("source", map[string]any{
			"languages": res.Languages, "identities": res.Identities, "ownerships": res.Ownerships,
		})
	}
	a.resetSummaryMissing()
	a.mergeSummaryMissing(res.MissingSummaries)
	a.requestSummaries(res.MissingSummaries)
	a.requestSourceTags(res.MissingSourceTags)
	a.pumpPipeline("manual reconcile")
	return res, nil
}

// shortHash is the 12-character prefix a log line uses to name a source.
func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func agentNotConfigured() error {
	return types.New(
		"not_implemented",
		"the agent is not configured: set OPENAI_API_KEY in the environment or a .env file (see .env.example), then restart",
		types.ErrNotImplemented,
	)
}

// RunningStep names the DOCUMENT step that is running for a source right now: "capture" while its
// bytes are being read, "outline" while its structure is built, "distill" while its claims are
// written. Empty when nothing runs for it.
//
// The CLUSTER step is NOT per source: it runs over the whole vault and is reported globally (see
// PipelineStatus). A source row that said "compile" would be claiming something that never runs for
// one source alone.
func (a *App) RunningStep(hash string) string {
	if a.captureRunning(hash) {
		return string(entities.StepCapture)
	}
	if a.treeRunning(hash) {
		return string(entities.StepOutline)
	}
	a.distillMu.Lock()
	defer a.distillMu.Unlock()
	if a.distillActive[hash] {
		return string(entities.StepDistill)
	}
	return ""
}

// captureRunning reports whether a capture is queued or in flight for this source.
func (a *App) captureRunning(hash string) bool {
	a.captureMu.Lock()
	defer a.captureMu.Unlock()
	return a.capturesInflight[hash]
}

// claimDistill marks a source hash as being derived; false when a run for it is already
// queued or running.
func (a *App) claimDistill(hash string) bool {
	a.distillMu.Lock()
	defer a.distillMu.Unlock()
	if a.distillActive[hash] {
		return false
	}
	a.distillActive[hash] = true
	a.claimedAt[hash] = time.Now()
	delete(a.queued, queueKey(entities.StepDistill, hash))
	return true
}

func (a *App) releaseDistill(hash string) {
	a.distillMu.Lock()
	delete(a.distillActive, hash)
	delete(a.claimedAt, hash)
	delete(a.queued, queueKey(entities.StepDistill, hash))
	delete(a.claimedRun, hash)
	a.distillMu.Unlock()
}

// setClaimRun remembers which run row belongs to a claim, so releasing the claim can close the row it
// leaves behind (a run that never reached a worker stays "running" for ever otherwise).
func (a *App) setClaimRun(hash string, runID ulid.ULID) {
	a.distillMu.Lock()
	if a.claimedRun == nil {
		a.claimedRun = map[string]ulid.ULID{}
	}
	a.claimedRun[hash] = runID
	a.distillMu.Unlock()
}

// markQueued records that a run for this source is waiting in a queue (not executing yet), so the
// difference between "queued" and "lost" stays visible and a stale claim can be told apart.
func (a *App) markQueued(hash string, waiting int) {
	a.distillMu.Lock()
	a.queued[queueKey(entities.StepDistill, hash)] = true
	a.distillMu.Unlock()
	if a.SourceLog != nil {
		a.SourceLog.Append(context.Background(), hash, interfaces.SourceLogEvent{
			At: time.Now(), Phase: "distill", Level: "info", Message: "run queued",
			Fields: map[string]any{"waiting": waiting},
		})
	}
	a.publish("progress", map[string]any{
		"hash": hash, "phase": "queued", "msg": fmt.Sprintf("waiting behind %d run(s)", waiting),
		"fields": map[string]any{"queued_ahead": waiting},
	})
}

// queueKey scopes "waiting in a queue" to ONE step of one source. A source can wait for an outline and
// for a distill at the same time (a new run is queued the moment the old one ends), and with one flag
// per source the tree worker's clearWaiting erased the distill's wait — which is how a claim of a live
// run came to look stale to the watchdog.
func queueKey(step entities.Step, hash string) string { return string(step) + "\x00" + hash }

// markWaiting records that this STEP of this source is waiting in a queue, and clearWaiting records that
// a worker took it over. The status report lists both, and "queued" is a different row from "running".
func (a *App) markWaiting(step entities.Step, hash string) {
	a.distillMu.Lock()
	defer a.distillMu.Unlock()
	if a.queued == nil {
		a.queued = map[string]bool{}
	}
	a.queued[queueKey(step, hash)] = true
}

func (a *App) clearWaiting(step entities.Step, hash string) {
	a.distillMu.Lock()
	delete(a.queued, queueKey(step, hash))
	a.distillMu.Unlock()
}

// markRunning records that a worker took the run over: it is executing now, not waiting. The claim
// timestamp stays — it is "claimed since", and a claim older than a whole run budget means the run
// behind it is gone (its worker died), whether it was waiting or executing.
func (a *App) markRunning(hash string) {
	a.distillMu.Lock()
	delete(a.queued, queueKey(entities.StepDistill, hash))
	a.distillMu.Unlock()
}

// releaseStaleClaims frees sources whose claim has no run behind it any more: not waiting in a
// queue, not executing, and older than a whole run budget. Without this a leaked claim shows
// a step in the UI forever while the source's log stays silent — the exact symptom that looks
// like a hang and is impossible to tell from a slow run.
func (a *App) releaseStaleClaims(now time.Time, maxAge time.Duration) int {
	a.distillMu.Lock()
	var stale []string
	for hash, at := range a.claimedAt {
		if a.queued[queueKey(entities.StepDistill, hash)] || now.Sub(at) < maxAge {
			continue
		}
		// A run that is EXECUTING is not stale, however long it takes: a big document with many parts
		// legitimately runs for minutes. Releasing it here used to cut the claim under a live run, open
		// the same source to a second run and reset the step light — 160 sources at once, observed.
		if a.executingStep(hash) != "" {
			continue
		}
		stale = append(stale, hash)
	}
	abandoned := make(map[string]ulid.ULID, len(stale))
	for _, hash := range stale {
		if id, ok := a.claimedRun[hash]; ok {
			abandoned[hash] = id
		}
		delete(a.distillActive, hash)
		delete(a.claimedAt, hash)
		delete(a.queued, queueKey(entities.StepDistill, hash))
		delete(a.claimedRun, hash)
	}
	a.distillMu.Unlock()
	for hash, runID := range abandoned {
		// The row was created before the queue hand-off; without this it stays "running" for ever.
		if err := a.startRun.Abandon(context.Background(), runID, "the claim was released — no worker was behind it"); err != nil {
			a.log.Warn("stale claim: run not closed", "hash", hash, "run", runID.String(), "err", err)
		}
	}
	for _, hash := range stale {
		a.log.Warn("released a claim with no run behind it — the source was stuck on a step",
			"hash", hash)
		// The claim is not the only thing that was stuck: the STEP RECORD still says running, and a
		// light that blinks with nothing behind it is what the reader sees as a contradiction.
		a.clearRunningSteps(hash, "the run behind this step is gone")
		if a.SourceLog != nil {
			a.SourceLog.Append(context.Background(), hash, interfaces.SourceLogEvent{
				At: now, Phase: "distill", Level: "warn", Message: "stale run claim released",
			})
		}
		a.publish("source", map[string]string{"hash": hash})
	}
	return len(stale)
}

// clearRunningSteps puts a source's document steps back to pending when nothing runs any more.
//
// A step recorded as `running` is the pipeline's word that work is in flight. When the run behind it
// is gone (an interrupted run, a released claim, a restart), that word becomes a lie the reader can
// SEE: a light that blinks forever next to a title bar that says something else. The record is the
// truth, so the record is what gets corrected — the source is then re-derived by the normal sweeps.
func (a *App) clearRunningSteps(hash, reason string) {
	if a.steps == nil {
		return
	}
	steps, err := a.steps.Steps(hash)
	if err != nil {
		return
	}
	for _, step := range entities.PipelineSteps {
		if !step.IsDocument() {
			continue // compile is not per source: the cluster step is reported globally
		}
		st, ok := steps[step]
		if !ok || st.State != entities.StepRunning {
			continue
		}
		if err := a.steps.Record(context.Background(), hash, step, entities.StepStatus{
			State: entities.StepPending, At: time.Now().UTC(), Detail: reason,
		}); err != nil && a.log != nil {
			a.log.Warn("could not clear a running step", "hash", hash, "step", step, "err", err)
		} else if a.log != nil {
			a.log.Warn("step recorded as running, but no run is behind it", "hash", hash, "step", step, "reason", reason)
		}
	}
}

// watchClaims runs the claim watchdog.
func (a *App) watchClaims() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	maxAge := a.cfg.Pipeline.RunTimeoutDuration() + time.Minute
	for {
		select {
		case <-a.quit:
			return
		case now := <-ticker.C:
			a.releaseStaleClaims(now, maxAge)
		}
	}
}

// maxStalledRuns is how many runs in a row may make no progress before the app stops re-deriving a
// source automatically. The point is not the number: it is that the automatic behaviour is bounded
// and says so, instead of a source restarting itself every few minutes (observed: a completed run
// re-queued itself because its settle put claims back into the backlog, over and over).
const maxStalledRuns = 2

// needsAttention reports whether a source is stalled: run after run without any progress.
func (a *App) needsAttention(hash string) bool {
	a.derivMu.Lock()
	defer a.derivMu.Unlock()
	return a.stalled[hash] >= maxStalledRuns
}

// noteRunProgress tracks what a run achieved. Progress is a NEW claim, or LESS work left than the run
// before (open parts for a document run, uncited claims for a cluster pass). Two runs without progress
// and the source stops being re-derived by itself — the log says so, and the user's next action (or a
// reset) clears it.
//
// It is what makes "continue while work is left" safe: a document whose parts keep producing no claim
// is work left, and without this guard that is a loop.
func (a *App) noteRunProgress(hash string, atoms, left int) {
	a.derivMu.Lock()
	prev, seen := a.progress[hash] // seen: the map tells us whether this source ran before
	a.progress[hash] = runProgress{atoms: atoms, left: left}
	stalled := a.stalled[hash]
	if !seen || atoms > 0 || left < prev.left {
		stalled = 0
	} else {
		stalled++
	}
	a.stalled[hash] = stalled
	a.derivMu.Unlock()
	if stalled >= maxStalledRuns {
		a.log.Warn("no progress in consecutive runs — stopping the automatic re-derivation for this source",
			"hash", hash, "runs", stalled, "left", left, "hint", "press Distill (or reset) to try again")
	}
}

// clearStalled forgets a source's stall state: the user asked for it again.
func (a *App) clearStalled(hash string) {
	a.derivMu.Lock()
	delete(a.stalled, hash)
	delete(a.progress, hash)
	a.derivMu.Unlock()
}

// maxRunRetries bounds how often one source's failed run is retried within a session.
const maxRunRetries = 2

// scheduleRetry re-queues a run whose failure was TRANSIENT (a timeout, a rate limit, a 5xx).
// Everything the run derived is kept — finished parts are skipped and compiled atoms stay on their
// pages — so the retry resumes instead of starting over. A permanent failure (a rejected key, an
// unsupported document) is NOT retried: that would only burn quota, and the start-up sweep or the
// user's next action is the honest way back.
func (a *App) scheduleRetry(job runJob, err error) {
	// A pass that ran out of turns is not a permanent failure either: a fresh run resumes exactly
	// where this one stopped (finished parts are skipped), and the bounded counter stops a loop.
	if !interfaces.IsTransient(err) && !strings.Contains(err.Error(), "max iterations") {
		return
	}
	a.retryMu.Lock()
	a.retries[job.hash]++
	attempt := a.retries[job.hash]
	a.retryMu.Unlock()
	if attempt > maxRunRetries {
		a.log.Warn("giving up on this source for now — the next start resumes it",
			"hash", job.hash, "attempts", attempt-1)
		return
	}
	wait := time.Duration(10*attempt) * time.Second
	a.log.Info("retrying a failed run", "hash", job.hash, "attempt", attempt, "in", wait.String())
	go func() {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-a.quit:
			return
		case <-timer.C:
		}
		a.requestDistill(job.hash)
	}()
}

// clearRetries forgets a source's failed attempts once a run of it succeeds.
func (a *App) clearRetries(hash string) {
	a.retryMu.Lock()
	delete(a.retries, hash)
	a.retryMu.Unlock()
}

// runWorker executes the runs of one operation. Document runs, queries and lints have their own
// worker, so a long document run never blocks a fast lint.
func (a *App) runWorker(queue chan runJob) {
	for {
		if queue == a.distillQueue {
			job, ok := a.takeDistill()
			if !ok {
				return
			}
			a.runJob(job, queue)
			continue
		}
		select {
		case <-a.quit:
			return
		case job := <-queue:
			a.runJob(job, queue)
		}
	}
}

// runJob executes one run, keeping the lint counter for the status report.
func (a *App) runJob(job runJob, queue chan runJob) {
	if queue == a.lintQueue {
		a.lintMu.Lock()
		a.lintRunning++
		a.lintMu.Unlock()
	}
	a.runOne(job)
	if queue == a.lintQueue {
		a.lintMu.Lock()
		a.lintRunning--
		a.lintMu.Unlock()
	}
}

// takeDistill hands the next document run to a worker: what a user asked for comes first, then the
// automatic runs. Waiting on both channels means a quiet priority queue never delays the rest.
func (a *App) takeDistill() (runJob, bool) {
	select {
	case job := <-a.distillFirst:
		a.markRunning(job.hash)
		return job, true
	default:
	}
	select {
	case <-a.quit:
		return runJob{}, false
	case job := <-a.distillFirst:
		a.markRunning(job.hash)
		return job, true
	case job := <-a.distillQueue:
		a.markRunning(job.hash)
		return job, true
	}
}

// queueDistill puts a run on a queue WITHOUT blocking the caller. A blocking send from inside a
// worker stalls that worker (the pipeline deadlocks when the queue is full and every worker waits),
// and a blocking send from the tree worker stops every tree behind it. If the queue is full, the
// hand-over happens in the background instead — the work is not lost and nobody waits.
func (a *App) queueDistill(queue chan runJob, job runJob) bool {
	waiting := len(queue)
	if queue == a.distillFirst {
		waiting = 0
	}
	select {
	case queue <- job:
		a.markQueued(job.hash, waiting)
		return true
	default:
	}
	a.log.Warn("run queue is full, handing the run over in the background",
		"hash", job.hash, "queued", len(queue))
	// The run is WAITING, not lost: mark it before the goroutine blocks, or the claim watchdog
	// releases it after one run budget and a second run for the source can start.
	a.markQueued(job.hash, len(queue))
	go func() {
		select {
		case queue <- job:
		case <-a.quit:
			a.releaseDistill(job.hash)
		}
	}()
	return true
}

func (a *App) runOne(job runJob) {
	ctx, cancel := a.runContextFor(job.op)
	defer cancel()
	run := job.runID.String()
	// A maintenance run (a curation, a re-sort, a lint pass) is registered as it starts and
	// cleared as it ends: the Running list shows what the app is doing, not only what it owes. The ops
	// that take the exclusive GATE are not marked here: for them "a worker took it" is not "it runs" —
	// they say `waiting` first and become `running` once they are inside the gate (the job case does it).
	if job.op != entities.RunPipeline && job.op != entities.RunQuery && job.op != entities.RunVocabulary {
		a.markRunRunning(job.runID)
	}
	defer a.clearRun(job.runID)
	status := "succeeded"
	failure := ""
	defer func() {
		// The failure text travels with the event: a run that dies must not do it invisibly
		// (the UI shows it instead of quietly dropping the spinner).
		a.publish("run", map[string]any{
			"run_id": run, "op": string(job.op), "step": string(job.step),
			// hash names what the run was about (empty for a query, a lint pass, a name curation).
			// The UI needs it to refresh only the source a run touched.
			"hash": job.hash, "status": status, "error": failure,
		})
	}()

	switch job.op {
	case entities.RunPipeline:
		// The DOCUMENT run (outline → distill). It takes the shared side of the gate, so the cluster
		// step cannot run while a document is being derived — and it does NOT touch the association
		// layer. The
		// budget starts INSIDE the gate: waiting for a long cluster pass is not work.
		if !job.reset && !a.stepBarrierAllows(entities.StepDistill) {
			// The source moved behind a newly added one. The run row exists, but the strict barrier
			// wins; abandon it and let the pump start it again when the outline stage is complete.
			_ = a.startRun.Abandon(context.Background(), job.runID, "waiting for every source to reach the outline step")
			a.releaseDistill(job.hash)
			a.log.Info("document run deferred: the global outline barrier is not complete", "hash", job.hash)
			return
		}
		var res usecases.DocumentRunResult
		release := a.enterDocStep(entities.StepDistill, job.hash)
		runCtx, runCancel := a.runContext()
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		r, err := a.documentRun.Execute(runCtx, job.runID, job.hash, job.reset)
		res = r
		runCancel()
		release()
		a.releaseDistill(job.hash)
		if err != nil {
			status = "failed"
			failure = err.Error()
			a.log.Warn("document run failed", "hash", job.hash, "run", run, "err", err)
			a.scheduleRetry(job, err)
			return
		}
		a.clearRetries(job.hash)
		a.log.Info("document run done", "hash", job.hash, "run", run,
			"atoms", res.Atoms, "written", res.Written, "iterations", res.Iterations, "tokens", res.TotalTokens,
			"parts_done", res.PartsDone, "parts_total", res.PartsTotal,
			"parts_left", res.PartsLeft)
		a.publish("source", map[string]any{"hash": job.hash})
		a.publish("clusters", map[string]any{"hash": job.hash, "atoms": res.Atoms})
		// Track what this run achieved, BEFORE deciding to continue: "progress" is a NEW claim, or
		// less work left (open parts) than the run before. It is res.Written, not res.Atoms: the
		// TOTAL says nothing about this run, and counting it made a source with old claims look
		// productive while it wrote nothing.
		a.noteRunProgress(job.hash, res.Written, res.PartsLeft)
		// A book does not fit into one run — and neither does a document whose parts have not produced
		// their claims yet. Continue while work is left; the stall guard bounds it, so a document that
		// yields no claim AND finishes no part stops after two runs and says so, instead of burning
		// quota in a loop.
		switch {
		case !res.Pending:
		case !a.cfg.Pipeline.AutoContinue:
		case a.needsAttention(job.hash):
			a.log.Warn("document derivation stops: no progress in consecutive runs",
				"hash", job.hash, "parts_left", res.PartsLeft, "atoms", res.Atoms,
				"hint", "press Distill (or reset) to try again")
		default:
			a.log.Info("document run continues", "hash", job.hash,
				"parts_left", res.PartsLeft, "atoms", res.Atoms)
			a.requestDistill(job.hash)
		}
		// The pump decides what follows: another distill run for this source (auto-continue), or —
		// only when EVERY source has finished distill — the cluster step.
		if !res.Pending {
			a.pumpPipeline("document run finished " + shortHash(job.hash))
			// The summary follows the claims: a complete derivation earns a fresh overview, and an
			// older one is replaced. It runs on its own worker, never inside this run. A RESET ends
			// with Pending=false but has just thrown the derivation away — asking for a summary there
			// would only produce a failed run.
			if !job.reset {
				a.requestSummary(job.hash)
				a.requestSourceTag(job.hash)
			}
		}
		// A reset that arrived WHILE this run was working has to be honoured after it: its flag is
		// explicit, so nothing has to be inferred from the derived state (guessing here once cost a
		// hot loop: a source that yields no atoms looked like "needs a run" and re-queued itself
		// thousands of times, saturating every document worker).
		a.resumePendingDerivation(job.hash)
	case entities.RunSummary:
		// The summary reads claims and writes a sidecar: the shared side of the gate, so it may run
		// beside a document step and never beside the cluster step.
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		a.markSummaryRunning(job.hash, true)
		var sres usecases.SourceSummaryResult
		err := a.sharedWrite(ctx, func(ctx context.Context) error {
			var sErr error
			sres, sErr = a.summary.Execute(ctx, job.runID, job.hash)
			return sErr
		})
		a.markSummaryRunning(job.hash, false)
		a.releaseSummary(job.hash)
		if err != nil {
			status = "failed"
			failure = err.Error()
			a.log.Warn("source summary failed", "hash", job.hash, "run", run, "err", err)
			return
		}
		a.completeSummary(job.hash)
		a.publish("source", map[string]any{"hash": job.hash, "summary": sres.Chars})
	case entities.RunQuery:
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		a.enterAsk()
		res, err := a.query.Execute(ctx, job.runID, job.question)
		a.leaveAsk()
		if err != nil {
			status = "failed"
			failure = err.Error()
			a.log.Warn("query failed", "run", run, "err", err)
			return
		}
		a.log.Info("query done", "run", run, "iterations", res.Iterations, "tokens", res.TotalTokens)
	case entities.RunLint:
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		res, err := a.lint.Execute(ctx, job.runID)
		if err != nil {
			status = "failed"
			failure = err.Error()
			a.log.Warn("lint failed", "run", run, "err", err)
			return
		}
		a.log.Info("lint done", "run", run, "findings", res.Findings)
		a.publish("findings", map[string]any{"count": res.Findings})
	case entities.RunTag:
		// Tagging writes labels and nothing else: the shared side of the gate, so it may run beside a
		// document step. It cannot overlap a save — a save is exclusive, and the two exclude each other.
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		if job.hash != "" {
			// A SOURCE's auto tags: derived from its claims after a complete distill (and backfilled
			// for a vault that predates the labels).
			var sres usecases.TagSourceResult
			err := a.sharedWrite(ctx, func(ctx context.Context) error {
				var tErr error
				sres, tErr = a.tagSource.Execute(ctx, job.runID, job.hash)
				return tErr
			})
			a.releaseSourceTag(job.hash)
			if err != nil {
				status = "failed"
				failure = err.Error()
				a.log.Warn("source auto tags failed", "hash", job.hash, "run", run, "err", err)
				return
			}
			a.publish("source", map[string]any{"hash": job.hash, "tags": len(sres.Tags)})
			return
		}
		var res usecases.TagResult
		err := a.sharedWrite(ctx, func(ctx context.Context) error {
			var tErr error
			res, tErr = a.tagPage.Execute(ctx, job.page)
			return tErr
		})
		if err != nil {
			status = "failed"
			failure = err.Error()
			a.log.Warn("tagging failed", "page", job.page.String(), "run", run, "err", err)
			return
		}
		a.log.Info("page tagged", "page", job.page.String(), "tags", len(res.Tags),
			"run", run, "tokens", res.Tokens)
		a.publish("wiki", map[string]any{"page": job.page.String()})
	case entities.RunVocabulary:
		if job.rebranch {
			// Re-deriving the TOPIC labels is a repair of the claim-topic vocabulary, not a curation: it
			// reads the claims, asks the model for each claim's subject, and rewrites only the label.
			// It writes claim files, so it takes the exclusive side: no document step may be writing
			// claims while their labels move.
			a.markMaintWaiting(job.runID, a.gateHolderReason("the topic repair rewrites claim labels"))
			a.log.Info("retopic: waiting for the gate", "run", run)
			a.gate.Lock()
			a.markRunRunning(job.runID)
			a.log.Info("retopic: gate acquired", "run", run)
			a.setMaintDetail(job.runID, "labelling the claims")
			runCtx, runCancel := a.runContext()
			a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
			res, err := a.rebranch.Execute(runCtx, job.runID)
			runCancel()
			a.gate.Unlock()
			if err != nil {
				status = "failed"
				failure = err.Error()
				a.log.Warn("rebranch failed", "run", run, "err", err)
				return
			}
			summary := fmt.Sprintf("%d claim(s) examined, %d re-labelled, %d left",
				res.Examined, res.Changed, res.Remaining)
			a.log.Info("claims re-labelled", "run", run, "summary", summary)
			a.publish("clusters", map[string]any{"vocabulary": "topics"})
			a.publish("vocabulary", map[string]any{"namespaces": []string{"topics: " + summary}})
			return
		}
		defer func() {
			a.vocabMu.Lock()
			a.vocabActive = false
			a.vocabMu.Unlock()
		}()
		// A curation is ONE unit: the baseline is adopted first (so the vault has one even when
		// the model fails), then the claim-topic vocabulary is curated.
		//
		// It rewrites claim names, so it must not run beside a document step: a distill writing
		// claims while their labels move is exactly the overlap the gate exists to prevent. The
		// whole unit holds the EXCLUSIVE side, questions included; the pipeline waits, and the run
		// is reported as waiting until the gate is actually free.
		namespaces := entities.VocabularyNamespaces()
		if job.namespace != "" {
			namespaces = []entities.VocabularyNamespace{job.namespace}
		}
		results := make([]usecases.CurateResult, 0, len(namespaces))
		a.markMaintWaiting(job.runID, a.gateHolderReason("the vocabulary curation rewrites claim names"))
		a.log.Info("vocabulary: waiting for the gate", "run", run)
		a.gate.Lock()
		defer a.gate.Unlock()
		a.markRunRunning(job.runID)
		a.log.Info("vocabulary: gate acquired", "run", run)
		a.publish("run", map[string]any{"run_id": run, "op": string(job.op), "step": string(job.step), "status": "running"})
		a.log.Info("vocabulary: curating", "run", run, "namespaces", len(namespaces))
		unitCtx, cancelUnit := context.WithTimeout(ctx, vocabularyUnitTimeout)
		defer cancelUnit()
		ctx = unitCtx
		if _, rErr := a.vocabularyReconcile.Execute(ctx); rErr != nil {
			a.log.Warn("vocabulary: the baseline could not be adopted", "err", rErr)
		}
		var err error
		for _, ns := range namespaces {
			a.log.Info("vocabulary: namespace", "namespace", ns)
			a.setMaintDetail(job.runID, string(ns))
			uc := a.curate
			uc.Namespace = ns
			// The QUESTIONS stay parallel — one job, several model calls in flight: that is latency,
			// not concurrency in the vault. The exclusive gate above covers the APPLY.
			uc.Parallel = vocabularyQuestionsAtOnce
			uc.OnPhase = func(phase string) { a.setMaintDetail(job.runID, string(ns)+" · "+phase) }
			uc.ApplyGate = func(ctx context.Context, fn func(context.Context) error) error {
				return fn(ctx)
			}
			r, e := uc.Execute(ctx, job.runID)
			// The partial result counts too: a chunk that merged names before the failure changed
			// the vault, and the summary the reader sees has to say so.
			results = append(results, r)
			if e != nil {
				err = e
				break
			}
		}
		// The summary is built from what the namespaces DID, whether the run then failed or not: the
		// model names only what it MERGED, so the canonical set is not its answer (reporting "527→0" was
		// the run describing its own bookkeeping as an empty vocabulary), and the merges applied before a
		// failure are real work the reader has to see.
		summary := []string{}
		for _, r := range results {
			touched, unit := r.Applied.Atoms, "claims"
			switch entities.VocabularyNamespace(r.Namespace) {
			case entities.VocabularyWikiTags:
				touched, unit = r.Applied.Pages, "pages"
			case entities.VocabularySourceTags:
				touched, unit = r.Applied.Sources, "sources"
			}
			line := fmt.Sprintf("%s: %d judged, %d merged (%d %s relabelled)",
				r.Namespace, r.Names, len(r.Mapping), touched, unit)
			// The check rides in the summary: a run that says "merged" and leaves the name in the vault
			// has to say that too, or the number reads like a result.
			if len(r.Residual) > 0 {
				line += fmt.Sprintf(" — %d absorbed name(s) STILL in the vault", len(r.Residual))
			}
			summary = append(summary, line)
		}
		if err != nil {
			status = "failed"
			failure = err.Error()
			a.log.Warn("vocabulary curation failed", "run", run, "err", err)
			// What it DID do is real: the merges before the failure were applied, so the UI has to be
			// told — otherwise the report keeps showing the vocabulary the run just changed.
			a.publish("clusters", map[string]any{"vocabulary": "partial"})
			a.publish("vocabulary", map[string]any{"namespaces": summary})
			return
		}
		a.log.Info("vocabulary curated", "run", run, "namespaces", strings.Join(summary, " · "))
		a.publish("clusters", map[string]any{"vocabulary": "all"})
		a.publish("vocabulary", map[string]any{"namespaces": summary})
	}
}

// refreshWorker re-fetches URL sources on the configured cadence. Changed pages are
// stored as new snapshots and run through the normal document path (capture → outline → distill).

// ScheduleURLs accepts a batch of URLs (e.g. a bookmarks file, one per line) and
// fetches them in the background. Each finished fetch emits an SSE event, so a long
// list streams into the inbox instead of blocking the request.
func (a *App) ScheduleURLs(urls []string) (int, error) {
	seen := make(map[string]bool, len(urls))
	accepted := 0
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" || seen[u] {
			continue
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			continue
		}
		seen[u] = true
		select {
		case a.urlQueue <- urlJob{URL: u}:
			accepted++
		default:
			return accepted, types.New("conflict", "url queue is full", types.ErrConflict)
		}
	}
	if accepted > 0 && a.log != nil {
		a.log.Info("url batch queued", "urls", accepted)
	}
	return accepted, nil
}

// ScheduleSitemapURLs claims the pages a sitemap declared and hands the NEW ones to the fetch queue.
// The sitemap is their owner: the rows carry its hash in ParentHash, and deleting the sitemap
// deletes them. The parent source is already written when this runs.
func (a *App) ScheduleSitemapURLs(parentHash string, urls []string) {
	if parentHash == "" || len(urls) == 0 || a.queueURLs.Index == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		a.gate.RLock()
		if _, err := a.queueURLs.Index.GetSourceByHash(ctx, parentHash); err != nil {
			a.gate.RUnlock()
			return
		}
		queued, err := a.queueURLs.ExecuteSitemap(ctx, parentHash, urls)
		a.gate.RUnlock()
		if err != nil {
			a.log.Warn("sitemap: pages not queued", "hash", parentHash, "err", err)
			return
		}
		if len(queued) == 0 {
			return
		}
		a.publish("source", map[string]any{"hash": parentHash, "links": len(queued)})
		for _, raw := range queued {
			select {
			case a.urlQueue <- urlJob{URL: raw, Origin: entities.SitemapOrigin, ParentHash: parentHash}:
			case <-a.quit:
				return
			}
		}
		a.log.Info("sitemap pages queued", "hash", parentHash, "links", len(queued))
	}()
}

// ScheduleFeedURLs hands the pages a feed declared to the fetch queue. They are ALREADY CLAIMED (the
// feed expansion claimed them, and stored the ones whose copy the feed carried), so this only
// enqueues — claiming again would find them known and queue nothing.
func (a *App) ScheduleFeedURLs(parentHash string, urls []string) {
	if parentHash == "" || len(urls) == 0 || a.addURL.Index == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		a.gate.RLock()
		_, parentErr := a.addURL.Index.GetSourceByHash(ctx, parentHash)
		a.gate.RUnlock()
		if parentErr != nil {
			return
		}
		a.publish("source", map[string]any{"hash": parentHash, "links": len(urls)})
		for _, raw := range urls {
			select {
			case a.urlQueue <- urlJob{URL: raw, Origin: entities.RSSOrigin, ParentHash: parentHash}:
			case <-a.quit:
				return
			}
		}
		a.log.Info("feed items queued", "hash", parentHash, "links", len(urls))
	}()
}

// ScheduleWikiURLs claims the links a page just declared and hands the NEW ones to the fetch queue.
// It runs OFF the request path: writing a page with a hundred links is a local write, and the hundred
// fetches are background work. Each link becomes a source row before the first fetch answers, so the
// reader sees the link — pending, then green, or red with the reason.
func (a *App) ScheduleWikiURLs(pageID string, urls []string) {
	if len(urls) == 0 || a.queueURLs.Index == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		a.gate.RLock()
		queued, err := a.queueURLs.Execute(ctx, pageID, urls)
		a.gate.RUnlock()
		if err != nil {
			a.log.Warn("wiki: links not queued", "page", pageID, "err", err)
			return
		}
		if len(queued) == 0 {
			return
		}
		// The claimed rows are visible before any fetch answers: the list says which links the page
		// brought, and the light says what happened to each of them.
		a.publish("source", map[string]any{"page": pageID, "links": len(queued)})
		for _, raw := range queued {
			select {
			case a.urlQueue <- urlJob{URL: raw, Origin: entities.WikiLinkOrigin, WikiPage: pageID}:
			default:
				a.log.Warn("url queue is full: the link was not fetched", "url", raw, "page", pageID)
			}
		}
		a.log.Info("wiki links queued", "page", pageID, "links", len(queued))
	}()
}

// ScheduleWikiTag derives a page's tags from its own title and prose. It runs OFF the request path: a
// save must not wait for a model call, and the tags arrive as a wiki event when they are there.
func (a *App) ScheduleWikiTag(pageID string) {
	if !a.agentAvailable || a.tagPage.Pages == nil {
		return
	}
	id, err := ulid.ParseStrict(pageID)
	if err != nil {
		return
	}
	runID, err := a.startRun.Execute(context.Background(), entities.RunTag,
		a.cfg.OpenAI.ModelCompile, "", pageID)
	if err != nil {
		a.log.Warn("tagging: cannot start run", "page", pageID, "err", err)
		return
	}
	a.markQueuedRun(runID, "tag")
	select {
	case a.tagQueue <- runJob{runID: runID, op: entities.RunTag, page: id}:
	default:
		// The queue is small and the runs are short: a page saved faster than the model answers waits
		// for the next save, and "tags are missing" is not a reason to grow goroutines.
		a.log.Info("tagging: queue is full, the page keeps its tags", "page", pageID)
		_ = a.startRun.Abandon(context.Background(), runID, "the tag queue was full")
		a.clearRun(runID)
	}
}

// ScheduleWikiReprocess clears a changed wiki page's old derivation in the background and only then
// pumps the source. The save request has already written the new page and its source representation;
// it must not wait for claim/tree deletion, which can take tens of seconds on a large page.
func (a *App) ScheduleWikiReprocess(hash string) {
	if hash == "" {
		return
	}
	go func() {
		err := a.ExclusiveWrite(context.Background(), "wiki reprocess", func(ctx context.Context) error {
			reset := a.resetSource
			reset.KeepTitle = true
			_, rErr := reset.Execute(ctx, hash)
			return rErr
		})
		if err != nil {
			a.log.Warn("wiki: cannot reset the old derivation", "hash", hash, "err", err)
			a.publish("source", map[string]any{"hash": hash})
			return
		}
		a.publish("source", map[string]any{"hash": hash})
		a.pumpPipeline("wiki page reprocessed")
	}()
}

// ScheduleWikiCleanup prunes what a saved wiki page stopped owning: the link sources it no longer
// declares, and an older representation it pointed at before the stable WikiHash. The save request
// has already written the new page; it must not wait for a delete, and a delete of a link used to
// walk the whole atom vault (a save with three removed links took 73 seconds).
//
// The pass re-reads the page, so two saves in a row, a link re-added before the job runs, or a page
// deleted in between all resolve to the same answer.
func (a *App) ScheduleWikiCleanup(pageID, staleHash string) {
	if pageID == "" {
		return
	}
	id, err := ulid.ParseStrict(pageID)
	if err != nil {
		return
	}
	go func() {
		var res usecases.PruneWikiLinksResult
		err := a.ExclusiveWrite(context.Background(), "wiki cleanup", func(ctx context.Context) error {
			var pErr error
			res, pErr = a.pruneWikiLinks.Execute(ctx, id, staleHash)
			return pErr
		})
		if err != nil {
			a.log.Warn("wiki: cleanup failed", "page", pageID, "err", err)
			return
		}
		if res.LinksRemoved == 0 && !res.StaleRemoved {
			return
		}
		a.log.Info("wiki cleanup done", "page", pageID, "links_removed", res.LinksRemoved,
			"stale_removed", res.StaleRemoved)
		// The same pair a single delete publishes: the source list changed and claims may have gone
		// with the link sources.
		a.publish("source", map[string]any{"page": pageID})
		a.publish("clusters", map[string]any{"page": pageID})
		a.pumpPipeline("wiki links pruned")
	}()
}

// urlWorker fetches queued URLs one at a time. Several workers run in parallel.
func (a *App) urlWorker() {
	for {
		select {
		case <-a.quit:
			return
		case job := <-a.urlQueue:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			// A URL fetch writes a source object and its row: shared gate, so it may run beside a
			// document step but never beside the cluster pass.
			a.gate.RLock()
			// A dependent link can outlive its owner in the queue (the owner was deleted while the
			// fetch was still owed). Do not resurrect it as an independent source: the owner and the
			// claimed row are both gone, so the job is over. The check sits inside the shared gate:
			// deletion takes the exclusive side and cannot slip between it and the fetch.
			if job.Origin != "" {
				_, childErr := a.addURL.Index.GetSourceByHash(ctx, entities.URLHash(job.URL))
				var parentErr error
				if job.ParentHash != "" {
					_, parentErr = a.addURL.Index.GetSourceByHash(ctx, job.ParentHash)
				}
				if childErr != nil || parentErr != nil {
					a.gate.RUnlock()
					cancel()
					continue
				}
			}
			// The human's own addition WINS over a wiki/sitemap/feed claim (job.Origin is empty for
			// both the single add and a pasted batch). A job with an owner stays a sub-source.
			fetch := a.addURL.Execute
			if job.Origin == "" {
				fetch = a.addURL.ExecuteManual
			}
			src, err := fetch(ctx, job.URL, false)
			a.gate.RUnlock()
			cancel()
			if err != nil {
				a.reportLinkFailure(job, err)
				continue
			}
			a.log.Info("url added", "url", job.URL, "hash", src.Hash)
			if len(src.SitemapURLs) > 0 {
				a.ScheduleSitemapURLs(src.Hash, src.SitemapURLs)
			}
			if len(src.RSSURLs) > 0 {
				a.ScheduleFeedURLs(src.Hash, src.RSSURLs)
			}
			a.onAdded(src.Hash)
		}
	}
}

// reportLinkFailure records a link that did not answer. A URL that arrived with a page (Origin set) is
// written down: its row stays, red, with the reason in its record, its metadata and its own log — a
// dead link is part of what the page claims, not something to lose in the process log. A URL from
// another intake keeps the old behaviour: the caller has the error and the queue reports the failure.
func (a *App) reportLinkFailure(job urlJob, cause error) {
	a.log.Warn("url intake failed", "url", job.URL, "err", cause)
	if job.Origin == "" || a.markURLFailed.Index == nil {
		a.publish("urls", map[string]any{"failed": 1, "url": job.URL})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a.gate.RLock()
	var (
		hash string
		err  error
	)
	if job.Origin == entities.SitemapOrigin {
		hash, err = a.markURLFailed.ExecuteSitemap(ctx, job.URL, job.ParentHash, cause)
	} else if job.Origin == entities.RSSOrigin {
		hash, err = a.markURLFailed.ExecuteFeed(ctx, job.URL, job.ParentHash, cause)
	} else {
		hash, err = a.markURLFailed.Execute(ctx, job.URL, job.WikiPage, cause)
	}
	a.gate.RUnlock()
	if err != nil {
		a.log.Warn("url failure not recorded", "url", job.URL, "err", err)
		a.publish("urls", map[string]any{"failed": 1, "url": job.URL})
		return
	}
	a.publish("source", map[string]any{"hash": hash})
	a.publish("urls", map[string]any{"failed": 1, "url": job.URL})
}

func (a *App) refreshWorker() {
	interval, err := time.ParseDuration(a.cfg.Capture.RefreshInterval)
	if err != nil || interval <= 0 {
		a.log.Info("url refresh disabled", "interval", a.cfg.Capture.RefreshInterval)
		return
	}
	// The ticker only decides how often the app LOOKS for sources that are due; the cadence itself
	// belongs to each source's url_checked_at, so restarting the app cannot postpone a refresh — and
	// cannot trigger a pass for everything at once either.
	tick := min(interval, time.Hour)
	a.log.Info("url refresh enabled", "cadence", interval.String(), "check", tick.String())
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-ticker.C:
			a.refreshNow("scheduled")
		}
	}
}

// refreshNow runs one refresh pass and schedules the changed snapshots.
func (a *App) refreshNow(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a.gate.RLock()
	res, err := a.refreshURLs.Execute(ctx)
	a.gate.RUnlock()
	if err != nil {
		a.log.Warn("url refresh failed", "reason", reason, "err", err)
		return
	}
	a.log.Info("url refresh done", "reason", reason,
		"checked", res.Checked, "changed", res.Changed, "failed", res.Failed,
		"blocked", res.Blocked, "attached", res.Attached,
		"discovered", len(res.Discovered), "removed", res.Removed)
	for _, s := range res.Sources {
		a.onAdded(s.Hash)
	}
	// A sitemap or a feed that declared new pages has already CLAIMED them (the refresh did that);
	// here they only need fetching. They arrive through the same queue as any other link, with their
	// owner set, so a failed fetch is recorded against that owner.
	a.ScheduleDiscoveredChildren(res.Discovered)
	// A changed page is a changed source: its old claims were corrected, so the association layer
	// has claims to regroup. Step 4 does that when the document steps are quiet.
	if len(res.ChangedURLs) > 0 {
		_, _ = a.requestCluster("url changed")
	}
	a.publish("urls", map[string]any{
		"checked": res.Checked, "changed": res.Changed, "failed": res.Failed,
		"discovered": len(res.Discovered), "removed": res.Removed,
	})
}

// ScheduleDiscoveredChildren fetches the NEW pages a feed or a sitemap declared. The refresh already
// claimed them (their rows exist with their owner); without this they would be listed as sources
// that never arrive. The per-source Refresh button and the scheduled pass share this path.
func (a *App) ScheduleDiscoveredChildren(children []usecases.DiscoveredChildren) {
	if a.urlQueue == nil {
		return
	}
	for _, group := range children {
		for _, raw := range group.URLs {
			select {
			case a.urlQueue <- urlJob{URL: raw, Origin: group.Origin, ParentHash: group.ParentHash}:
			case <-a.quit:
				return
			}
		}
	}
}

// runContext bounds one run (document or cluster pass) so it can never hang forever.
// runningDistills counts the sources a document run is in flight for.
func (a *App) runningDistills() int {
	a.distillMu.Lock()
	defer a.distillMu.Unlock()
	return len(a.distillActive)
}

// runBudget caps the whole retry ladder of one model call: without it, a per-call timeout of 300 s
// times four attempts is twenty minutes for a single call — which, behind a serialised worker, looks
// exactly like "the app hangs". The gateway stops retrying when the budget is used up.
func runBudget(cfg config.Config) time.Duration {
	if d, err := time.ParseDuration(cfg.OpenAI.Timeout); err == nil && d > 0 {
		return d
	}
	return 120 * time.Second
}

// runContextFor is the run's own ceiling. A document run is bounded by pipeline.run_timeout; a name
// curation asks ONE question per namespace over the WHOLE catalog, and that is minutes of model
// time — a ten-minute ceiling would cut it off in the middle of its first question and leave the pass
// half-finished for ever.
func (a *App) runContextFor(op entities.RunOp) (context.Context, context.CancelFunc) {
	if op == entities.RunVocabulary {
		return context.WithTimeout(context.Background(), vocabularyGateWait+vocabularyUnitTimeout)
	}
	if op == entities.RunCluster {
		timeout, err := time.ParseDuration(a.cfg.Pipeline.ClusterTimeout)
		if err == nil && timeout > 0 {
			return context.WithTimeout(context.Background(), timeout)
		}
	}
	return a.runContext()
}

func (a *App) runContext() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	timeout, err := time.ParseDuration(a.cfg.Pipeline.RunTimeout)
	if err != nil || timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// maybeDistill runs the document steps for a source that has a tree but no atoms.
func (a *App) maybeDistill(hash string) {
	if !a.cfg.Pipeline.AutoDistill || !a.agentAvailable {
		return
	}
	has, err := a.sourceHasAtoms.Execute(context.Background(), hash)
	if err != nil {
		a.log.Warn("auto-distill: cannot read the source state", "hash", hash, "err", err)
		return
	}
	if has {
		// A reset can sweep the derivation away while a run is still writing; that run's own end
		// re-checks the source, so this is not a dead end — but say that it was skipped.
		a.log.Info("auto-distill skipped: the source has claims", "hash", hash)
		return
	}
	if !a.claimDistill(hash) {
		// A run for this source is queued or running. It re-checks on its way out, so the source
		// is not left behind — the old code stayed silent here, which looked like a hang.
		a.log.Info("auto-distill deferred: a run for this source is already in flight", "hash", hash)
		return
	}
	a.startDistill(hash)
}

// markPendingDerivation remembers that a source needs a fresh derivation once its running run ends.
func (a *App) markPendingDerivation(hash string) {
	a.derivMu.Lock()
	a.pendingDeriv[hash] = true
	a.derivMu.Unlock()
}

// resumePendingDerivation queues the derivation a reset asked for while a run was in flight.
func (a *App) resumePendingDerivation(hash string) {
	a.derivMu.Lock()
	wanted := a.pendingDeriv[hash]
	delete(a.pendingDeriv, hash)
	a.derivMu.Unlock()
	if !wanted {
		return
	}
	a.log.Info("resuming the derivation a reset asked for", "hash", hash)
	a.requestDistill(hash)
}

// distillFree reports whether no run holds this source right now.
func (a *App) distillFree(hash string) bool {
	a.distillMu.Lock()
	defer a.distillMu.Unlock()
	return !a.distillActive[hash]
}

// startDistill creates and queues the run for a source whose claim is already taken.
func (a *App) startDistill(hash string) {
	runID, err := a.startRun.Execute(context.Background(), entities.RunPipeline, a.cfg.OpenAI.ModelCompile, entities.StepDistill, hash)
	if err != nil {
		a.releaseDistill(hash)
		a.log.Warn("auto-distill: cannot start run", "hash", hash, "err", err)
		return
	}
	a.setClaimRun(hash, runID)
	a.queueDistill(a.distillQueue, runJob{runID: runID, op: entities.RunPipeline, hash: hash})
	a.log.Info("auto-distill scheduled", "hash", hash, "queued", len(a.distillQueue))
	a.publish("source", map[string]string{"hash": hash})
}

// startupRepair restores consistency BEFORE the HTTP server starts. The reader must never see a
// cluster the vault already says does not exist: the index is a cache, and the files are the truth.
//
// The index is rebuilt when the consistency check finds a disagreement (a cluster record without its
// rows, a row without its record, a member whose claim is gone) or when the source index is empty.
func (a *App) startupRepair() {
	if a.reindex.Index == nil || a.db == nil {
		return
	}
	// The audit first: a run a previous process left "running" has no worker behind it any more.
	if n, err := a.startRun.AbandonLeftovers(context.Background(), "interrupted by a restart"); err != nil {
		a.log.Warn("startup: leftover runs not closed", "err", err)
	} else if n > 0 {
		a.log.Info("startup: closed runs a previous process left running", "runs", n)
	}

	a.log.Info("startup: checking vault consistency")
	started := time.Now()
	needsRepair, reason, checkErr := a.reindex.NeedsRepair(context.Background())
	if checkErr != nil {
		a.log.Warn("startup: consistency check failed", "err", checkErr)
	}

	runReindex := func(why string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		a.gate.Lock()
		res, err := a.reindex.Execute(ctx)
		a.gate.Unlock()
		cancel()
		if err != nil {
			a.log.Warn("startup: reindex failed", "reason", why, "err", err)
			return
		}
		a.log.Info("startup: reindexed the vault",
			"reason", why, "sources", res.Scanned, "atoms", res.Atoms,
			"clusters", res.Clusters, "clusters_removed", res.ClustersRemoved,
			"atoms_removed", res.AtomsRemoved, "steps", res.Steps, "parts_pruned", res.PartsPruned, "skipped", res.Skipped,
			"dur", time.Since(started).Round(time.Millisecond).String())
	}

	if needsRepair {
		a.log.Warn("startup: the association layer is inconsistent — rebuilding it before serving", "reason", reason)
		runReindex(reason)
	} else if checkErr == nil {
		// Say that the check RAN, not only when it failed: "no news" is not evidence of a check.
		a.log.Info("startup: vault consistency ok", "dur", time.Since(started).Round(time.Millisecond).String())
	}

	count, countErr := persistence.CountChecked(a.db, "sources")
	if countErr != nil {
		a.log.Warn("startup: source count failed", "err", countErr)
		return
	}
	if count == 0 {
		runReindex("the source index is empty")
	}
}

// startupSweep makes a fresh start self-healing: it repairs referential damage, builds trees
// for sources that never got one, then derives sources that have a tree but no atoms.
func (a *App) startupSweep() {
	// Ask records written before citations became ordinary Markdown still carry [[claim:...]]
	// in their raw files; normalise them once at start, so the vault is right on disk.
	if a.query.Asks != nil {
		if n, err := a.query.Asks.Normalize(context.Background()); err != nil {
			a.log.Warn("startup: normalise saved asks failed", "err", err)
		} else if n > 0 {
			a.log.Info("startup: normalised saved Ask citations", "files", n)
		}
	}

	// Orphaned rows (a provenance row without its atom) make a source count claims its detail
	// view cannot show. Repair them at start, and say how many.
	if a.maintenance != nil {
		if n, err := a.maintenance.PruneOrphans(context.Background()); err != nil {
			a.log.Warn("startup: prune orphans failed", "err", err)
		} else if n > 0 {
			a.log.Warn("startup: removed orphaned index rows", "rows", n)
		}
	}
	// Nothing survives a restart: a step recorded as running belongs to a run that no longer exists.
	// Left alone it blinks in the UI forever, next to a title bar that reports an idle pipeline.
	if a.steps != nil {
		if recs, err := a.steps.Records(); err != nil {
			a.log.Warn("startup: read the source records", "err", err)
		} else {
			for hash, rec := range recs {
				for _, step := range entities.PipelineSteps {
					if st, ok := rec.Steps[step]; ok && st.State == entities.StepRunning {
						a.clearRunningSteps(hash, "interrupted by a restart")
					}
				}
			}
		}
	}
	// Everything that QUEUES A RUN waits a moment. A start that immediately begins deriving, outlining
	// and compiling leaves the reader no window to trigger something by hand first — and the machine
	// then competes with itself over the gate. The repairs above stay immediate (they queue nothing);
	// the sweeps wait.
	select {
	case <-a.quit:
		return
	case <-time.After(startupRunDelay):
	}
	// One global pump: it finds the first step not complete for every source and queues only
	// that step. The separate sweeps are deliberately not called here — they would jump ahead of
	// the barrier (a source with an outline would be distilled while another still lacks capture).
	a.pumpPipeline("startup")
}

// sweepMissingAtoms distills sources that have a built tree but no atoms.
func (a *App) sweepMissingAtoms() {
	srcs, err := a.missingAtoms.Execute(context.Background())
	if err != nil {
		a.log.Warn("auto-distill sweep failed", "err", err)
		return
	}
	if len(srcs) == 0 {
		return
	}
	a.log.Info("auto-distill sweep", "sources", len(srcs))
	for _, s := range srcs {
		select {
		case <-a.quit:
			return
		default:
		}
		a.requestDistill(s.Hash)
	}
}

// sweepOpenDerivations resumes a derivation that is not finished: text parts without a recorded
// done. This is a 500-page book between runs, and the reason a restart continues it.
func (a *App) sweepOpenDerivations() {
	srcs, err := a.openParts.Execute(context.Background())
	if err != nil {
		a.log.Warn("open-derivation sweep failed", "err", err)
		return
	}
	if len(srcs) == 0 {
		return
	}
	a.log.Info("open-derivation sweep", "sources", len(srcs))
	for _, s := range srcs {
		select {
		case <-a.quit:
			return
		default:
		}
		a.requestDistill(s.Hash)
	}
}

// requestDistill claims a source and queues its run (the sweeps and the retry use this).
func (a *App) requestDistill(hash string) {
	if !a.stepBarrierAllows(entities.StepDistill) {
		a.log.Debug("distill waits for every source to finish the outline", "hash", hash)
		return
	}
	a.autoDistill(hash)
}

// autoDistill starts the source's document run without re-checking the global barrier: the pump
// already established it for the whole pass.
// autoDistill claims one source and queues its run. It reports whether it claimed — the pump uses that
// to bound how much of the backlog one pass takes on.
func (a *App) autoDistill(hash string) bool {
	if a.needsAttention(hash) {
		a.log.Warn("auto-distill skipped: this source made no progress in its last runs", "hash", hash)
		return false
	}
	if !a.claimDistill(hash) {
		// Every pass walks the whole backlog, so this is the normal case for a source that already
		// runs: at Info it wrote 18,000 lines in half an hour and buried the log it belongs in.
		a.log.Debug("auto-distill skipped: a run for this source is already in flight", "hash", hash)
		return false
	}
	a.startDistill(hash)
	return true
}

// distillWindow is how many sources ONE pump pass may claim for the distill step.
//
// A claim says "this source is being worked on", and the queue hands it to `parallel_runs` workers.
// Claiming the whole backlog (150 sources for 2 workers) filled the queue (64), left a blocked goroutine
// per overflow and let the claim watchdog expire claims that were merely waiting. The pump runs after
// EVERY run, so a small window keeps the workers fed, the Job Queue honest and the claims young.
func (a *App) distillWindow() int {
	if a.cfg.Pipeline.ParallelRuns <= 0 {
		return 2
	}
	return 2*a.cfg.Pipeline.ParallelRuns + 2
}

func checkWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tk-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
