// Package registry is one of the two places that knows concrete types (the other is
// cmd). It builds the adapters, wires the use cases and returns the handler.
package registry

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/handlers"
	"github.com/TheFranconianCoder/thought-keeper/internal/api/router"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/asks"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/clusters"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/favicons"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
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
	VocabularyCurate usecases.CurateVocabulary
	// TagWikiPage derives a page's tags from its title and prose.
	TagWikiPage usecases.TagWikiPage
	// SourceSummary writes a source's overview from its claims.
	SourceSummary usecases.SourceSummary
	// TagSource derives a source's auto tags from its claims.
	TagSource           usecases.TagSource
	RebranchClaims      usecases.RebranchClaims
	VocabularyReconcile usecases.ReconcileVocabulary
	Intake              usecases.IntakeSource
	BuildTree           usecases.BuildTree
	StartRun            usecases.StartRun
	DocumentRun         usecases.DocumentRun
	Reconcile           usecases.ReconcileVault
	Reindex             usecases.ReindexVault
	Capture             usecases.CaptureSource
	// ClusterPass is step 4: the titleless association of claims.
	ClusterPass usecases.ClusterPass
	Cleanup     usecases.Cleanup
	Query       usecases.QueryKnowledge
	Lint        usecases.LintKnowledge
	AddURL      usecases.AddURL
	RefreshURLs usecases.RefreshURLs
	// QueueURLs claims the links a wiki page brings before anything is fetched; MarkURLFailed records
	// the ones whose fetch did not happen, so a dead link stays visible instead of vanishing.
	QueueURLs      usecases.QueueURLs
	MarkURLFailed  usecases.MarkURLFailed
	GetSource      usecases.GetSource
	SourceHasAtoms usecases.SourceHasAtoms
	SourceLog      interfaces.SourceLog
	// Steps is the source step tracker (the <hash>.meta records).
	Steps                interfaces.StepTracker
	SourcesWithOpenParts usecases.SourcesWithOpenParts
	SourcesWithoutAtoms  usecases.SourcesWithoutAtoms
	// SourcesWithUnclustered is the backlog of step 4.
	SourcesWithUnclustered usecases.SourcesWithUnclustered
	SourcesWithoutTree     usecases.SourcesWithoutTree
	Maintenance            interfaces.Maintenance
	ResetSource            usecases.ResetSource
	// PruneWikiLinks removes what a saved page stopped owning, in the background.
	PruneWikiLinks usecases.PruneWikiLinks
	AgentAvailable bool
}

// Build wires the adapters, the use cases and the HTTP handler.
func Build(
	cfg config.Config,
	db *sql.DB,
	status handlers.StatusProvider,
	onAdded func(hash string),
	onRemoved func(hash string),
	onTag func(pageID string),
	onReprocess func(hash string),
	onCleanup func(pageID, staleHash string),
	onDiscovered func([]usecases.DiscoveredChildren),
	scheduler handlers.Runner,
	pipeline handlers.PipelineProvider,
	clusterQueue handlers.ClusterQueue,
	reconciler handlers.Reconciler,
	gate router.Gate,
	hub *stream.Hub,
	log *slog.Logger,
) Deps {
	store := objects.New(cfg.SourcesDir())
	// The URL cadences: `capture.refresh_interval` is for a page or a sitemap, and
	// `capture.feed_refresh_interval` for a feed's item list. The worker checks for due sources far
	// more often; each interval belongs to the source's url_checked_at.
	refreshMinAge, _ := time.ParseDuration(cfg.Capture.RefreshInterval)
	feedRefreshMinAge, _ := time.ParseDuration(cfg.Capture.FeedRefreshInterval)
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
	clock := gateways.SystemClock{}
	runner := gateways.ExecRunner{}
	languages := langdetect.Detector{}

	fetchTimeout, _ := time.ParseDuration(cfg.Fetch.Timeout)
	fetcher := gateways.NewFetcher(gateways.FetcherConfig{
		Timeout:      fetchTimeout,
		MaxBytes:     cfg.Fetch.MaxBytes,
		UserAgent:    cfg.Fetch.UserAgent,
		AllowPrivate: cfg.Fetch.AllowPrivate,
	})
	converter := gateways.ReadabilityConverter{}

	extractor := extract.New(runner, extract.Config{
		PDFTextTool:         cfg.PDF.TextTool,
		Rasterizer:          cfg.OCR.Rasterizer,
		Tesseract:           cfg.OCR.Tesseract,
		Languages:           cfg.OCR.Languages,
		DPI:                 cfg.OCR.DPI,
		MinTextCharsPerPage: cfg.OCR.MinTextCharsPerPage,
		MaxPages:            cfg.OCR.MaxPages,
		OCR:                 cfg.OCR.Enabled,
		Log:                 log,
	})

	timeout, _ := time.ParseDuration(cfg.OpenAI.Timeout)
	retryBackoff, err := time.ParseDuration(cfg.OpenAI.RetryBackoff)
	if err != nil || retryBackoff <= 0 {
		retryBackoff = 2 * time.Second
	}
	retryPolicy := interfaces.RetryPolicy{Attempts: cfg.OpenAI.Retries, Base: retryBackoff, Budget: timeout}
	gw, agentOK := gateways.NewOpenAIAgent(gateways.OpenAIConfig{
		BaseURL: cfg.OpenAI.BaseURL,
		APIKey:  cfg.OpenAI.APIKey,
		Timeout: timeout,
		Headers: cfg.OpenAI.Headers,
		Log:     log,
		Retry:   retryPolicy,
	})

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
	intake := usecases.IntakeSource{Store: store, Index: index, SourceLog: sourceLog, Clock: clock, Steps: steps, Languages: languages}
	list := usecases.ListSources{Index: index}
	facets := usecases.ListSourceFacets{Index: index}
	sourceChildren := usecases.SourceChildren{Index: index}
	setSourceTags := usecases.SetSourceTags{Index: index, Tags: steps, SourceLog: sourceLog, Clock: clock}
	get := usecases.GetSource{Index: index}
	maintenanceRepo := sqlc.NewMaintenanceRepository(db)
	reindex := usecases.ReindexVault{
		Store: store, Index: index, Atoms: atomRepo, AtomStore: atomStore,
		Maintenance: maintenanceRepo, SourceLog: sourceLog,
		Trees: trees, Clock: clock, Log: log, Steps: steps, Parts: partRepo,
		ClusterStore: clusterStore, ClusterRepo: clusterRepo,
	}
	capture := usecases.CaptureSource{
		Store: store, Index: index, Extractor: extractor, Steps: steps, Languages: languages,
		SourceLog: sourceLog, Clock: clock, Log: log,
	}
	buildTree := usecases.BuildTree{
		Store: store, Index: index, Trees: trees, Extractor: extractor,
		SourceLog: sourceLog, Clock: clock, Gateway: gw, Model: cfg.OpenAI.ModelCompile,
		Steps: steps, Languages: languages,
	}
	rebuildTrees := usecases.RebuildTrees{Index: index, BuildTree: buildTree}

	faviconStore := favicons.New(cfg.FaviconsDir())
	addURL := usecases.AddURL{Fetch: fetcher, Convert: converter, Store: store, Index: index, Favicons: faviconStore, MinTextChars: cfg.Capture.MinTextChars, Clock: clock, Log: log, Steps: steps, Languages: languages, SourceLog: sourceLog}
	queueURLs := usecases.QueueURLs{Index: index, Steps: steps, Clock: clock, Log: log}
	markURLFailed := usecases.MarkURLFailed{Index: index, Steps: steps, SourceLog: sourceLog, Clock: clock, Log: log}
	attachURL := usecases.AttachURLSnapshot{
		Convert: converter, Extractor: extractor, Store: store, Index: index, Steps: steps,
		SourceLog: sourceLog, Languages: languages, Clock: clock, Log: log,
		// An attachment is a local file the human chose; the PDF ceiling is the honest bound, since a
		// page saved as PDF is the largest form an attachment takes.
		MaxBytes: cfg.PDF.MaxBytes,
	}
	vocabularyReport := usecases.VocabularyReport{
		Atoms: atomRepo, Wiki: wikiStore, Sources: index, Vocab: vocabStore,
	}
	vocabularyReconcile := usecases.ReconcileVocabulary{
		Report: vocabularyReport, Vocab: vocabStore, Clock: clock, Log: log,
	}
	vocabularyCurate := usecases.CurateVocabulary{
		Report: vocabularyReport, Atoms: atomRepo, Wiki: wikiStore, Sources: steps,
		Gateway: gw, Model: cfg.OpenAI.ModelCompile, Apply: true,
		Vocab: vocabStore, AtomStore: atomStore,
		Runs: runRepo, Clock: clock, Log: log,
	}
	// A wiki page's tags are DERIVED from its own title and prose, and the vocabulary it writes into
	// is the one the curation maintains.
	tagWikiPage := usecases.TagWikiPage{
		Gateway: gw, Model: cfg.OpenAI.ModelCompile, Pages: wikiStore, Vocab: vocabStore,
		Clock: clock, Log: log,
	}
	sourceSummary := usecases.SourceSummary{
		Index: index, Atoms: atomRepo, Trees: trees, Store: store,
		Gateway: gw, Model: cfg.OpenAI.ModelCompile, Language: cfg.Language.Summary,
		Runs: runRepo, SourceLog: sourceLog, Clock: clock, Log: log,
	}
	tagSource := usecases.TagSource{
		Index: index, Atoms: atomRepo, Tags: steps,
		Gateway: gw, Model: cfg.OpenAI.ModelCompile, Vocab: vocabStore,
		Runs: runRepo, SourceLog: sourceLog, Clock: clock, Log: log,
	}
	deleteSource := usecases.DeleteSource{
		Store: store, Index: index, Atoms: atomStore, AtomRepo: atomRepo,
		SourceLog: sourceLog, Clock: clock, Log: log, Steps: steps,
	}
	refreshURLs := usecases.RefreshURLs{
		Fetch: fetcher, Convert: converter, Store: store, Index: index, Atoms: atomRepo,
		Favicons: faviconStore, Clock: clock, Log: log, Steps: steps, Languages: languages,
		SourceLog: sourceLog, MinAge: refreshMinAge, FeedMinAge: feedRefreshMinAge,
		MinTextChars: cfg.Capture.MinTextChars,
		// A sitemap is a complete list: a page that left it is deleted by its owner. A feed never
		// deletes; the same use case decides from the parent's marker.
		Delete: deleteSource,
	}
	migrateURLAddresses := usecases.MigrateURLAddresses{
		Index: index, Store: store, Atoms: atomRepo, AtomStore: atomStore, Steps: steps, Log: log,
	}
	listWiki := usecases.ListWikiPages{Pages: wikiStore}
	getWiki := usecases.GetWikiPage{Pages: wikiStore}
	sourceContent := usecases.SourceContent{Index: index, Store: store}
	sourceSidecars := usecases.SourceSidecars{Index: index, Store: store}
	fixURLs := usecases.FixURLKinds{Index: index, Declared: store, Steps: steps, Log: log}
	sourceFavicon := usecases.SourceFavicon{Index: index, Favicons: faviconStore}
	getTree := usecases.GetTree{Index: index}
	getNode := usecases.GetNode{Trees: trees, Index: index, Store: store}

	listAtoms := usecases.ListAtoms{AtomRepo: atomRepo, Clusters: clusterRepo}
	listBranches := usecases.ListClaimTopics{AtomRepo: atomRepo}
	listAsks := usecases.ListAsks{Asks: askStore, Atoms: atomRepo, Sources: index}
	getAsk := usecases.GetAsk{Asks: askStore, Atoms: atomRepo, Sources: index}
	deleteAsk := usecases.DeleteAsk{Asks: askStore}
	getAtom := usecases.GetAtom{AtomRepo: atomRepo, Clusters: clusterRepo}
	sourceAtoms := usecases.SourceAtoms{AtomRepo: atomRepo, Clusters: clusterRepo}
	getRun := usecases.GetRun{Runs: runRepo}
	startRun := usecases.StartRun{Runs: runRepo, Clock: clock}

	distill := usecases.DistillSource{
		Index: index, Trees: trees, Store: store, Atoms: atomStore, AtomRepo: atomRepo,
		PartRepo: partRepo, SourceLog: sourceLog, Steps: steps, Vocab: vocabStore,
		PartsPerRun: cfg.Pipeline.PartsPerRun, Parallel: cfg.Pipeline.ParallelParts,
		Model: cfg.OpenAI.ModelCompile, Language: cfg.Language.Claims,
		MaxIterations:   cfg.OpenAI.MaxIterations,
		ReasoningEffort: cfg.OpenAI.ReasoningEffortDistill, Clock: clock, Log: log,
	}
	resetSource := usecases.ResetSource{
		Index: index, Trees: trees, SourceLog: sourceLog, Clock: clock, Log: log, Steps: steps,
	}
	cleanSource := usecases.CleanSource{
		Parts: partRepo, SourceLog: sourceLog, Index: index,
		Atoms: atomRepo, AtomStore: atomStore, Store: store, Tags: steps, Clock: clock, Log: log,
	}
	buildTree.Clean = cleanSource
	buildTree.Parts = partRepo
	rebuildTrees = usecases.RebuildTrees{Index: index, BuildTree: buildTree}
	resetSource.Clean = cleanSource
	resetSource.SourceLog = sourceLog
	// A replacement snapshot (an attach or a forced fetch) is a new source text: the derivation that
	// belonged to the old one is reset.
	attachURL.Reset = resetSource
	refreshURLs.Reset = resetSource

	saveWiki := usecases.SaveWikiPage{
		Pages: wikiStore, Store: store, Index: index, Steps: steps,
		SourceLog: sourceLog, Clock: clock, Log: log,
	}
	deleteWiki := usecases.DeleteWikiPage{Pages: wikiStore, Index: index, Delete: deleteSource, Log: log}
	pruneWikiLinks := usecases.PruneWikiLinks{Pages: wikiStore, Index: index, Delete: deleteSource, Log: log}

	// Step 4: the titleless association of claims. It is deterministic — no model, no naming.
	clusterPass := usecases.ClusterPass{
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
	pendingCluster := usecases.SourcesWithUnclustered{Index: index}
	documentRun := usecases.DocumentRun{
		Reset: resetSource, Capture: capture, BuildTree: buildTree, Distill: distill,
		SourceLog: sourceLog, Runs: runRepo, Clock: clock, Model: cfg.OpenAI.ModelCompile, Log: log,
	}
	reconcile := usecases.ReconcileVault{
		Index: index, Steps: steps, Store: store, Clean: cleanSource,
		SourceLog: sourceLog, Clock: clock, Log: log, Limit: cfg.Pipeline.ReconcileBatch,
		Languages: languages, Trees: trees,
	}
	query := usecases.QueryKnowledge{
		Atoms: atomRepo, Clusters: clusterRepo, Search: searchRepo, Index: index, Store: store,
		Asks: askStore, Runs: runRepo,
		Model: cfg.OpenAI.ModelReason, MaxIterations: cfg.OpenAI.MaxIterations,
		ReasoningEffort: cfg.OpenAI.ReasoningEffortReason, Clock: clock, Log: log,
	}
	reorganizeAtoms := usecases.ReorganizeAtoms{Atoms: atomStore, AtomRepo: atomRepo, Log: log}
	quoteClaimCode := usecases.QuoteClaimCode{Atoms: atomStore, AtomRepo: atomRepo, Clock: clock, Log: log}
	collapseAtoms := usecases.CollapseDuplicateAtoms{
		Atoms: atomStore, AtomRepo: atomRepo, Clusters: clusterRepo, Clock: clock, Log: log,
	}
	// The duplicate check is a maintenance question the PWA asks twice at start-up; one scan serves
	// both callers, and a collapse invalidates the answer.
	duplicates := &cachedDuplicates{uc: collapseAtoms}
	lintExec := usecases.LintKnowledge{
		Lint: lintRepo, Atoms: atomRepo, Runs: runRepo, Clock: clock, Log: log,
	}
	cleanup := usecases.Cleanup{
		Clusters: clusterRepo, Store: clusterStore, Atoms: atomRepo, Lint: lintRepo,
		Clock: clock, Log: log,
	}
	if agentOK {
		distill.Gateway = gw
		query.Gateway = gw
		documentRun.Distill = distill
	}
	listFindings := usecases.ListFindings{Lint: lintRepo}
	hasAtoms := usecases.SourceHasAtoms{Index: index}
	withoutAtoms := usecases.SourcesWithoutAtoms{Index: index}
	openParts := usecases.SourcesWithOpenParts{Index: index}
	withoutTree := usecases.SourcesWithoutTree{Index: index}
	searchUC := usecases.Search{Search: searchRepo}

	h := router.New(router.Deps{
		Status:    status,
		Pipeline:  handlers.PipelineHandler{Pipeline: pipeline},
		Cluster:   handlers.ClusterHandler{Runs: clusterQueue},
		Reconcile: handlers.ReconcileHandler{Runs: reconciler},
		Sources: handlers.SourcesHandler{
			Lister: list, Counter: list, Facets: facets, ChildLister: sourceChildren, TagSetter: setSourceTags,
			Getter: get, Distill: scheduler, Summary: scheduler, Status: scheduler, Delete: deleteSource,
			Capturer: scheduler, Sidecars: sourceSidecars, FixURLs2: fixURLs, Attacher: attachURL,
			Content: sourceContent, Favicon: sourceFavicon, Log: sourceLog, Reset: scheduler, OnRemoved: onRemoved,
			OnAttached: onAdded,
			Mark:       scheduler,
		},
		Add: handlers.AddHandler{Intake: intake, URL: addURL, OnAdded: onAdded, Children: scheduler},
		URL: handlers.URLHandler{
			Refresh:      refreshURLs,
			Bulk:         scheduler,
			OnChanged:    onAdded,
			OnDiscovered: onDiscovered,
			Addresses:    migrateURLAddresses, Mark: scheduler,
		},
		Reindex: handlers.ReindexHandler{Reindex: reindex, Mark: scheduler},
		Vocabulary: handlers.VocabularyHandler{
			Report: &cachedVocabulary{uc: vocabularyReport}, Consume: scheduler,
		},
		Tree: handlers.TreeHandler{Tree: getTree, Node: getNode, Rebuilder: rebuildTrees, Builder: buildTree, Mark: scheduler},
		Atoms: handlers.AtomHandler{
			Atoms: listAtoms, ClaimTopics: listBranches, GetAtom: getAtom, SourceAtoms: sourceAtoms, Runs: getRun,
			AtomRelated: usecases.RelatedAtoms{Atoms: atomRepo, Clusters: clusterRepo},
			AtomLayout:  reorganizeAtoms, AtomReorganize: reorganizeAtoms,
			AtomDuplicates: duplicates, AtomCollapse: duplicates, QuoteCode: quoteClaimCode, Mark: scheduler,
		},
		Wiki: handlers.WikiHandler{
			List: listWiki, Get: getWiki, Save: saveWiki, Delete: deleteWiki,
			OnChanged:   func(_, hash string) { onAdded(hash) },
			OnReprocess: onReprocess,
			OnCleanup:   onCleanup,
			OnTag:       onTag, OnDeleted: onRemoved, URLs: scheduler, OnURLRemoved: onRemoved,
		},
		Search: handlers.SearchHandler{Search: searchUC},
		Asks:   handlers.AskHandler{Lister: listAsks, Getter: getAsk, Deleter: deleteAsk},
		Query:  handlers.QueryHandler{Query: scheduler},
		Lint:   handlers.LintHandler{Lint: scheduler, Findings: listFindings, Cleanup: cleanup, Mark: scheduler},
		Events: handlers.SSEHandler{Hub: hub},
		Gate:   gate,
		Log:    log,
	}, web.Handler())

	return Deps{
		Handler:          h,
		VocabularyCurate: vocabularyCurate,
		TagWikiPage:      tagWikiPage,
		SourceSummary:    sourceSummary,
		TagSource:        tagSource,
		RebranchClaims: usecases.RebranchClaims{
			Atoms: atomRepo, AtomStore: atomStore, Vocab: vocabStore, Gateway: gw,
			Model: cfg.OpenAI.ModelCompile, Runs: runRepo, Clock: clock, Log: log,
			MaxClaims: 12000, Parallel: 4, Apply: true,
		},
		VocabularyReconcile:    vocabularyReconcile,
		Intake:                 intake,
		Capture:                capture,
		BuildTree:              buildTree,
		StartRun:               startRun,
		DocumentRun:            documentRun,
		Reconcile:              reconcile,
		Reindex:                reindex,
		ClusterPass:            clusterPass,
		Cleanup:                cleanup,
		Query:                  query,
		Lint:                   lintExec,
		AddURL:                 addURL,
		RefreshURLs:            refreshURLs,
		QueueURLs:              queueURLs,
		MarkURLFailed:          markURLFailed,
		GetSource:              get,
		SourceHasAtoms:         hasAtoms,
		SourceLog:              sourceLog,
		Steps:                  steps,
		SourcesWithOpenParts:   openParts,
		SourcesWithoutAtoms:    withoutAtoms,
		SourcesWithUnclustered: pendingCluster,
		SourcesWithoutTree:     withoutTree,
		Maintenance:            maintenanceRepo,
		ResetSource:            resetSource,
		PruneWikiLinks:         pruneWikiLinks,
		AgentAvailable:         agentOK,
	}
}

var _ = entities.StepCluster
