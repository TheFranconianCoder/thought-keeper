// Package registry is one of the two places that knows concrete types (the other is
// cmd). It builds the adapters, wires the use cases and returns the handler.
package registry

import (
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/handlers"
	"github.com/TheFranconianCoder/thought-keeper/internal/api/router"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/asks"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/clusters"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/commands"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/favicons"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/interests"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/langdetect"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcelog"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
	treeStore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/vocabulary"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/wiki"
	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
	"github.com/TheFranconianCoder/thought-keeper/internal/web"
)

// Deps is what the composition root gets back: the HTTP handler and the use cases the lifecycle
// itself needs (watcher intake/tree, run scheduling/execution).
type Deps struct {
	Handler http.Handler
	// VocabularyCurate asks the model what the claim-topic vocabulary should become and applies it.
	VocabularyCurate usecases.Vocabulary
	// TagWikiPage derives a page's tags from its title and prose.
	TagWikiPage usecases.Vocabulary
	// SourceSummary writes a source's overview from its claims.
	SourceSummary usecases.Sources
	// TagSource derives a source's auto tags from its claims.
	TagSource           usecases.Vocabulary
	RebranchClaims      usecases.Claims
	VocabularyReconcile usecases.Vocabulary
	Intake              usecases.Sources
	BuildTree           usecases.Trees
	StartRun            usecases.Runs
	DocumentRun         usecases.Derivation
	Reconcile           usecases.Maintenance
	Reindex             usecases.Maintenance
	// PruneCommands enforces the assistant's retention in the same tick as the consistency job.
	PruneCommands usecases.Commands
	Capture       usecases.Derivation
	// ClusterPass is step 4: the titleless association of claims.
	ClusterPass usecases.Clusters
	Cleanup     usecases.Maintenance
	Query       usecases.Query
	Lint        usecases.Lint
	AddURL      usecases.URLs
	RefreshURLs usecases.URLs
	// QueueURLs claims the links a wiki page brings before anything is fetched; MarkURLFailed records
	// the ones whose fetch did not happen, so a dead link stays visible instead of vanishing.
	QueueURLs      usecases.URLs
	MarkURLFailed  usecases.URLs
	GetSource      usecases.Sources
	SourceHasAtoms usecases.Sources
	SourceLog      interfaces.SourceLog
	// Steps is the source step tracker (the <hash>.meta records).
	Steps                interfaces.StepTracker
	SourcesWithOpenParts usecases.Sources
	SourcesWithoutAtoms  usecases.Sources
	// SourcesWithUnclustered is the backlog of step 4.
	SourcesWithUnclustered usecases.Sources
	SourcesWithoutTree     usecases.Sources
	Maintenance            interfaces.Maintenance
	ResetSource            usecases.Derivation
	// PruneWikiLinks removes what a saved page stopped owning, in the background.
	PruneWikiLinks usecases.Wiki
	// MatchSourceInterests is the automatism: a new or freshly distilled source is compared with
	// the reader's rules and a strong match joins the read-later queue.
	MatchSourceInterests usecases.ReadLater
	AgentAvailable       bool
	// PDFAvailable and OCRAvailable are the probes the Status lens shows; the extractor was built with
	// these tools, so a config apply that changes them must replace the Deps to change the answer.
	PDFAvailable bool
	OCRAvailable bool
	// BrowserAvailable says whether the local rendering engine was found. The reader sees it in
	// Status, and it explains why a JavaScript page is a bookmark.
	BrowserAvailable bool
	BrowserTool      string
}

// Readiness is the start's repair state: the API is up while the index is rebuilt, reads answer from
// the vault and every change waits for a consistent index.
type Readiness interface {
	Repairing() bool
	RepairProgress(phase string, done, total int)
}

// Build wires the adapters, the use cases and the HTTP handler.
func Build(
	cfg config.Config,
	db *sql.DB,
	status handlers.StatusProvider,
	configProvider handlers.ConfigProvider,
	configWriter handlers.ConfigWriter,
	onAdded func(hash string),
	onRemoved func(hash string),
	onTag func(pageID string),
	onReprocess func(hash string),
	onCleanup func(pageID, staleHash string),
	onReset func(hash, reason string),
	onDiscovered func([]usecases.DiscoveredChildren),
	scheduler handlers.Runner,
	pipeline handlers.PipelineProvider,
	clusterQueue handlers.ClusterQueue,
	reconciler handlers.Reconciler,
	gate router.Gate,
	ready Readiness,
	hub *stream.Hub,
	log *slog.Logger,
) Deps {
	store := objects.New(cfg.SourcesDir())
	// The URL cadences: `capture.refresh_interval` is for a page or a sitemap, and
	// `capture.feed_refresh_interval` for a feed's item list. The worker checks for due sources far
	// more often; each interval belongs to the source's url_checked_at.
	refreshMinAge := durationOr(cfg.Capture.RefreshInterval, 168*time.Hour, log, "capture.refresh_interval")
	feedRefreshMinAge := durationOr(cfg.Capture.FeedRefreshInterval, 24*time.Hour, log, "capture.feed_refresh_interval")
	index := sqlc.NewSourceRepository(db)
	trees := treeStore.New(cfg.SourcesDir())
	atomStore := atoms.New(cfg.AtomsDir())
	atomRepo := sqlc.NewAtomRepository(db)
	clusterStore := clusters.New(cfg.ClustersDir())
	clusterRepo := sqlc.NewClusterRepository(db)
	askStore := asks.New(cfg.AsksDir())
	vocabStore := vocabulary.New(cfg.VocabularyDir())
	wikiStore := wiki.New(cfg.WikiDir())
	runRepo := sqlc.NewRunRepository(db)
	lintRepo := sqlc.NewLintRepository(db)
	searchRepo := sqlc.NewSearchRepository(db, persistence.HasFTS5(db))
	commandStore := commands.New(cfg.CommandsDir())
	clock := gateways.SystemClock{}
	runner := gateways.ExecRunner{}
	pdfAvailable := runner.Available(cfg.PDF.TextTool)
	ocrAvailable := runner.Available(cfg.OCR.Rasterizer) && runner.Available(cfg.OCR.Tesseract)
	languages := langdetect.Detector{}

	fetchTimeout := durationOr(cfg.Fetch.Timeout, 30*time.Second, log, "fetch.timeout")
	fetcher := gateways.NewFetcher(gateways.FetcherConfig{
		Timeout:      fetchTimeout,
		MaxBytes:     cfg.Fetch.MaxBytes,
		UserAgent:    cfg.Fetch.UserAgent,
		AllowPrivate: cfg.Fetch.AllowPrivate,
		MinTextChars: cfg.Capture.MinTextChars,
	})
	converter := gateways.ReadabilityConverter{}

	// The browser is OPTIONAL and is used whenever it is installed: a JavaScript page is read from
	// its rendered DOM, and the converter above writes the Markdown either way. nil here means the
	// platform has no engine (Windows) and the plain fetch stays the whole story.
	browserTimeout := durationOr(cfg.Browser.Timeout, 30*time.Second, log, "browser.timeout")
	browserWait := durationOr(cfg.Browser.Wait, 5*time.Second, log, "browser.wait")
	engine := gateways.NewLightpanda(gateways.LightpandaConfig{
		Path:            cfg.Browser.Path,
		Timeout:         browserTimeout,
		Wait:            browserWait,
		WaitUntil:       cfg.Browser.WaitUntil,
		MaxBytes:        cfg.Fetch.MaxBytes,
		UserAgent:       cfg.Fetch.UserAgent,
		AllowPrivate:    cfg.Fetch.AllowPrivate,
		ObeyRobots:      cfg.Browser.ObeyRobots,
		LoadStylesheets: cfg.Browser.LoadStylesheets,
		MinTextChars:    cfg.Capture.MinTextChars,
	}, runner)
	var renderer interfaces.Renderer
	if engine != nil {
		renderer = engine
		log.Info("browser rendering is on", "engine", engine.Name())
	} else {
		log.Info("browser rendering is off: no lightpanda binary",
			"path", cfg.Browser.Path, "hint", "install lightpanda or set browser.path")
	}

	extractor := extract.New(runner, extract.Config{
		PDFTextTool:         cfg.PDF.TextTool,
		Rasterizer:          cfg.OCR.Rasterizer,
		Tesseract:           cfg.OCR.Tesseract,
		Languages:           cfg.OCR.Languages,
		DPI:                 cfg.OCR.DPI,
		MinTextCharsPerPage: cfg.OCR.MinTextCharsPerPage,
		MaxPages:            cfg.PDF.MaxPages,
		OCRMaxPages:         cfg.OCR.MaxPages,
		OCR:                 cfg.OCR.Enabled,
		Log:                 log,
	})

	timeout := durationOr(cfg.OpenAI.Timeout, 60*time.Second, log, "openai.timeout")
	retryBackoff, err := time.ParseDuration(cfg.OpenAI.RetryBackoff)
	if err != nil || retryBackoff <= 0 {
		retryBackoff = 2 * time.Second
	}
	retryPolicy := interfaces.RetryPolicy{Attempts: cfg.OpenAI.Retries, Base: retryBackoff, Budget: timeout}
	agent, agentOK := gateways.NewOpenAIAgent(gateways.OpenAIConfig{
		BaseURL: cfg.OpenAI.BaseURL,
		APIKey:  cfg.OpenAI.APIKey,
		Timeout: timeout,
		Headers: cfg.OpenAI.Headers,
		Log:     log,
		Retry:   retryPolicy,
	})
	// The gateway is an INTERFACE on purpose: a typed nil (*OpenAIAgent)(nil) in an interface field is
	// NOT nil, so every "if uc.Gateway != nil" guard would pass and the first call would dereference
	// nil. Without a key the field stays a nil interface, and the agent-less paths keep working.
	var gw interfaces.AgentGateway
	if agentOK {
		gw = agent
	}

	sourceLog := sourcelog.New(cfg.SourcesDir())
	// Every line of a source's log is pushed as it is written: the overlay shows the run live.
	sourceLog.OnAppend = func(hash string, e interfaces.SourceLogEvent, line string) {
		hub.Publish("log", map[string]string{"hash": hash, "line": line})
		switch e.Phase {
		case "capture", "outline", "distill", "cluster", "settle", "reset":
			fields := map[string]any{}
			for _, k := range []string{
				"part", "atoms", "source_atoms", "tokens",
				"parts_done", "parts_total", "parts_left", "atoms_left",
			} {
				if v, ok := e.Fields[k]; ok {
					fields[k] = v
				}
			}
			hub.Publish("progress", map[string]any{
				"hash": hash, "phase": e.Phase, "level": e.Level, "msg": e.Message, "fields": fields,
			})
		}
	}
	partRepo := sqlc.NewPartRepository(db)
	meta := sourcemeta.New(cfg.SourcesDir())
	steps := sourcemeta.NewTracker(meta, index, log)
	favorites := usecases.Favorites{Index: index, Shelf: steps, Wiki: wikiStore, Clock: clock}

	// The reader's standing rules. The files under interests/ are the truth; the preview reads the
	// index for the calibration, and the ingest path matches one source in memory.
	interestStore := interests.New(cfg.InterestsDir())
	interestSignals := sqlc.NewInterestRepository(db, persistence.HasFTS5(db))
	readLater := usecases.ReadLater{
		Store: interestStore, Counter: index, Shelf: steps, Signals: interestSignals,
		Index: index, Atoms: atomRepo, Clock: clock,
	}
	maintenanceRepo := sqlc.NewMaintenanceRepository(db)
	var repairProgress func(phase string, done, total int)
	var repairing func() bool
	if ready != nil {
		repairProgress = ready.RepairProgress
		repairing = ready.Repairing
	}
	maintenance := usecases.Maintenance{
		Store: store, Index: index, Atoms: atomRepo, AtomStore: atomStore,
		MaintenanceRepo: maintenanceRepo, SourceLog: sourceLog, Lint: lintRepo,
		Trees: trees, Clock: clock, Log: log, Steps: steps, Parts: partRepo, Shelf: index,
		ClusterStore: clusterStore, ClusterRepo: clusterRepo,
		Limit: cfg.Pipeline.ReconcileBatch, Languages: languages,
		Progress: repairProgress,
	}
	derivation := usecases.Derivation{
		Store: store, Index: index, Extractor: extractor, Steps: steps, Languages: languages,
		SourceLog: sourceLog, Trees: trees, PartRepo: partRepo, Atoms: atomRepo, AtomStore: atomStore,
		Vocab: vocabStore, Runs: runRepo, Clock: clock, Log: log,
		PartsPerRun: cfg.Pipeline.PartsPerRun, Parallel: cfg.Pipeline.ParallelParts,
		MaxIterations: cfg.OpenAI.MaxIterations, Language: cfg.Language.Claims,
		ReasoningEffort: cfg.OpenAI.ReasoningEffortDistill, Model: cfg.OpenAI.ModelCompile,
	}
	treesUC := usecases.Trees{
		Store: store, Index: index, Trees: trees, Extractor: extractor,
		SourceLog: sourceLog, Clock: clock, Gateway: gw, Model: cfg.OpenAI.ModelCompile,
		Steps: steps, Languages: languages,
	}

	faviconStore := favicons.New(cfg.FaviconsDir())
	// A page's and a source's tags are DERIVED, and the vocabulary they write into is the one the
	// curation maintains.
	vocabulary := usecases.Vocabulary{
		Atoms: atomRepo, AtomStore: atomStore, Wiki: wikiStore, Reader: index, Sources: steps,
		Tags: steps, Index: index, Vocab: vocabStore, Runs: runRepo, SourceLog: sourceLog,
		Gateway: gw, Model: cfg.OpenAI.ModelCompile, Apply: true, Clock: clock, Log: log,
	}
	sources := usecases.Sources{
		Store: store, Pages: wikiStore, Index: index, Atoms: atomRepo, AtomStore: atomStore,
		CommandStore: commandStore, Tags: steps, Shelf: index, Favicons: faviconStore,
		Trees: trees, Convert: converter, Extractor: extractor,
		Gateway: gw, SourceLog: sourceLog, Steps: steps, Languages: languages, Runs: runRepo,
		Clock: clock, Log: log, Model: cfg.OpenAI.ModelCompile, Language: cfg.Language.Summary,
		MaxBytes: cfg.PDF.MaxBytes,
	}
	urls := usecases.URLs{
		Fetch: fetcher, Convert: converter, Render: renderer, Links: gateways.HTMLLinkExtractor{}, Store: store, Index: index,
		Atoms: atomRepo, AtomStore: atomStore,
		Favicons: faviconStore, Steps: steps, Languages: languages, SourceLog: sourceLog,
		Clock: clock, Log: log, MinTextChars: cfg.Capture.MinTextChars,
		MinAge: refreshMinAge, FeedMinAge: feedRefreshMinAge,
		// A sitemap is a complete list: a page that left it is deleted by its owner. A feed never
		// deletes; the same use case decides from the parent's marker.
		Delete: sources.DeleteOwned,
	}

	asks := usecases.Asks{Store: askStore, Atoms: atomRepo, Sources: index}
	commandRetention := durationOr(cfg.Assistant.Retention, 7*24*time.Hour, log, "assistant.retention")
	commands := usecases.Commands{
		Store: commandStore, Index: index, Atoms: atomRepo, Wiki: wikiStore, Vocab: vocabStore,
		Lexical: searchRepo, Clock: clock, Log: log,
		MaxItems: cfg.Assistant.MaxItems, ScanLimit: cfg.Assistant.ScanLimit,
		Retention: commandRetention,
	}
	claims := usecases.Claims{
		Atoms: atomRepo, AtomStore: atomStore, Clusters: clusterRepo,
		// The rebranch pass is the one claim repair that runs to completion when it is started.
		MaxClaims: 12000, Parallel: 4, Apply: true,
	}
	runs := usecases.Runs{Store: runRepo, Clock: clock}

	derivation.Tags = steps
	maintenance.Clean = derivation.Clean
	treesUC.Clean = derivation.Correct
	treesUC.Parts = partRepo
	// A replacement snapshot (an attach or a forced fetch) is a new source text: the derivation that
	// belonged to the old one is reset.
	sources.Reset = derivation.Reset
	urls.Reset = derivation.Reset

	wiki := usecases.Wiki{
		Pages: wikiStore, Store: store, Index: index, Steps: steps,
		SourceLog: sourceLog, DeleteSource: sources.DeleteOwned, Clock: clock, Log: log,
	}

	// Step 4: the titleless association of claims. It is deterministic — no model, no naming.
	clusters := usecases.Clusters{
		Atoms: atomRepo, Clusters: clusterRepo, Store: clusterStore,
		Settings: usecases.ClusterSettings{
			NeighborK:          cfg.Cluster.NeighborK,
			MinClaimScore:      cfg.Cluster.MinClaimScore,
			MergeMinInternal:   cfg.Cluster.MergeMinInternal,
			MergeMinGap:        cfg.Cluster.MergeMinGap,
			SplitMinCoreSize:   cfg.Cluster.SplitMinCoreSize,
			TopicSpanLimit:     cfg.Cluster.TopicSpanLimit,
			MaxClusterClaims:   cfg.Cluster.MaxClusterClaims,
			AttachMinScore:     cfg.Cluster.AttachMinScore,
			OverlapMinEdges:    cfg.Cluster.OverlapMinEdges,
			OverlapMax:         cfg.Cluster.OverlapMax,
			CrossTopicMinEdges: cfg.Cluster.CrossTopicMinEdges,
		},
		Clock: clock, Log: log, Runs: runRepo, Model: "",
	}
	pendingCluster := sources
	query := usecases.Query{
		Atoms: atomRepo, Clusters: clusterRepo, Lexical: searchRepo, Index: index, Store: store,
		Asks: askStore, Runs: runRepo,
		Model: cfg.OpenAI.ModelReason, MaxIterations: cfg.OpenAI.MaxIterations,
		ReasoningEffort: cfg.OpenAI.ReasoningEffortReason, Clock: clock, Log: log,
	}
	claims.CommandStore = commandStore
	claims.Vocab = vocabStore
	claims.Runs = runRepo
	claims.Clock = clock
	claims.Log = log
	// The duplicate check is a maintenance question the PWA asks twice at start-up; one scan serves
	// both callers, and a collapse invalidates the answer.
	duplicates := &cachedDuplicates{uc: claims}
	lintUC := usecases.Lint{
		Store: lintRepo, Atoms: atomRepo, Runs: runRepo, Clock: clock, Log: log,
	}

	if agentOK {
		derivation.Gateway = gw
		query.Gateway = gw
		// The assistant reads an utterance, it does not write the vault; a wrong key must not take
		// the deterministic parser with it.
		commands.Gateway = gw
		// The rebranch pass is the one claim repair that asks the model; without a gateway it stays
		// a dry run of the deterministic part.
		claims.Gateway = gw
		claims.Model = cfg.OpenAI.ModelCompile
		commands.Model = cfg.OpenAI.ModelCompile
	}
	hasAtoms, withoutAtoms, openParts, withoutTree := sources, sources, sources, sources

	h := router.New(router.Deps{
		Status:       status,
		Config:       configProvider,
		ConfigWriter: configWriter,
		Pipeline:     handlers.PipelineHandler{Pipeline: pipeline},
		Cluster:      handlers.ClusterHandler{Runs: clusterQueue},
		Reconcile:    handlers.ReconcileHandler{Runs: reconciler},
		Sources: handlers.SourcesHandler{
			Lister: sources.List, Counter: sources.Count, Facets: sources.Facets, ChildLister: sources.Children,
			Queue:     sources.ReadLaterCounts,
			TagSetter: sources.Tag,
			Favorite:  favorites.SetSource, ReadLater: readLater.Set, Interests: readLater.ForSource,
			Getter: sources.Get, Distill: scheduler, Summary: scheduler, Status: scheduler, Delete: sources.Delete,
			Capturer: scheduler, Sidecars: sources.Sidecars, Attacher: sources.Attach,
			Content: sources.Content, Rendered: sources.Rendered, Sidecar: sources.Sidecar,
			Favicon: sources.Favicon, Log: sourceLog, Reset: scheduler, OnRemoved: onRemoved,
			OnAttached:   onAdded,
			Mark:         scheduler,
			Commands:     sources.Commands,
			LinkResolver: sources.ResolveLinks,
		},
		Add: handlers.AddHandler{Intake: sources.Intake, URL: urls.Add, URLManual: urls.AddManual, OnAdded: onAdded, OnReplaced: onReset, Children: scheduler},
		URL: handlers.URLHandler{
			Refresh: urls.Refresh, RefreshAll: urls.RefreshAll,
			RefreshOne: urls.RefreshOne, RefreshOneForce: urls.RefreshOneForce,
			Bulk:         scheduler,
			OnChanged:    onAdded,
			OnReplaced:   onReset,
			OnDiscovered: onDiscovered,
			Mark:         scheduler,
		},
		Reindex: handlers.ReindexHandler{Reindex: maintenance.Reindex, Mark: scheduler},
		Vocabulary: handlers.VocabularyHandler{
			Report: (&cachedVocabulary{uc: vocabulary}).Report, Consume: scheduler,
		},
		Tree: handlers.TreeHandler{Tree: treesUC.Tree, Node: treesUC.Node, Rebuilder: treesUC.Rebuild, Builder: treesUC.Build, Mark: scheduler},
		Atoms: handlers.AtomHandler{
			Atoms: claims.List, ClaimTopics: claims.Topics, GetAtom: claims.Get, SourceAtoms: claims.BySource,
			Runs:           runs.Get,
			AtomRelated:    claims.Related,
			AtomDuplicates: duplicates, AtomCollapse: duplicates, Mark: scheduler,
			Commands: claims.Commands,
		},
		Wiki: handlers.WikiHandler{
			List: wiki.List, Get: wiki.Get, Save: wiki.Save, Delete: wiki.Delete, Favorite: favorites.SetWiki,
			OnChanged:   func(_, hash string) { onAdded(hash) },
			OnReprocess: onReprocess,
			OnCleanup:   onCleanup,
			OnTag:       onTag, OnDeleted: onRemoved, URLs: scheduler, OnURLRemoved: onRemoved,
		},
		Interests: handlers.InterestHandler{
			Lister: readLater.Interests, Saver: readLater.SaveInterest, Deleter: readLater.DeleteInterest,
			Previewer: readLater.PreviewInterest, Rejecter: readLater.RejectInterest,
		},
		Search: handlers.SearchHandler{Search: query.Search},
		Asks:   handlers.AskHandler{Lister: asks.List, Getter: asks.Get, Deleter: asks.Delete},
		Commands: handlers.CommandHandler{
			Runner: commands.Run, Lister: commands.List, Getter: commands.Get, Deleter: commands.Delete,
			Notifier: hub,
		},
		Query:     handlers.QueryHandler{Query: scheduler},
		Lint:      handlers.LintHandler{Lint: scheduler, Findings: lintUC.Findings, Mark: scheduler},
		Events:    handlers.SSEHandler{Hub: hub},
		Gate:      gate,
		Repairing: repairing,
		Log:       log,
	}, web.Handler())

	return Deps{
		Handler:                h,
		VocabularyCurate:       vocabulary,
		TagWikiPage:            vocabulary,
		SourceSummary:          sources,
		TagSource:              vocabulary,
		RebranchClaims:         claims,
		VocabularyReconcile:    vocabulary,
		Intake:                 sources,
		Capture:                derivation,
		BuildTree:              treesUC,
		StartRun:               runs,
		DocumentRun:            derivation,
		Reconcile:              maintenance,
		PruneCommands:          commands,
		Reindex:                maintenance,
		ClusterPass:            clusters,
		Cleanup:                maintenance,
		Query:                  query,
		Lint:                   lintUC,
		AddURL:                 urls,
		RefreshURLs:            urls,
		QueueURLs:              urls,
		MarkURLFailed:          urls,
		GetSource:              sources,
		SourceHasAtoms:         hasAtoms,
		SourceLog:              sourceLog,
		Steps:                  steps,
		SourcesWithOpenParts:   openParts,
		SourcesWithoutAtoms:    withoutAtoms,
		SourcesWithUnclustered: pendingCluster,
		SourcesWithoutTree:     withoutTree,
		Maintenance:            maintenanceRepo,
		ResetSource:            derivation,
		PruneWikiLinks:         wiki,
		MatchSourceInterests:   readLater,
		AgentAvailable:         agentOK,
		PDFAvailable:           pdfAvailable,
		OCRAvailable:           ocrAvailable,
		BrowserAvailable:       renderer != nil,
		BrowserTool:            browserToolName(cfg.Browser.Path),
	}
}

var _ = entities.StepCluster

// browserToolName is the binary the status report names: the configured path's file name, or the
// engine's own name when the default lookup is used. The status is a capability report, not a path
// listing; the full path is in the start-up log.
func browserToolName(configured string) string {
	if name := strings.TrimSpace(configured); name != "" {
		return filepath.Base(name)
	}
	return gateways.EngineLightpanda
}

// durationOr reads a configured duration. A value that does not parse is reported and replaced by
// the sane default: a typo in the YAML must not silently disable a cadence.
func durationOr(value string, fallback time.Duration, log *slog.Logger, name string) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		log.Warn("registry: the configured duration is not usable; using the default",
			"key", name, "value", value, "default", fallback.String(), "err", err)
		return fallback
	}
	return d
}
