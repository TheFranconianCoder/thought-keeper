// Package config loads the YAML configuration with ${ENV} expansion and defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the whole application configuration.
type Config struct {
	App      AppConfig      `yaml:"app"`
	Server   ServerConfig   `yaml:"server"`
	Vault    VaultConfig    `yaml:"vault"`
	Index    IndexConfig    `yaml:"index"`
	OpenAI   OpenAIConfig   `yaml:"openai"`
	Pipeline PipelineConfig `yaml:"pipeline"`
	Cluster  ClusterConfig  `yaml:"cluster"`
	Capture  CaptureConfig  `yaml:"capture"`
	// Browser is the OPTIONAL local browser that renders a page before the converter reads it. It
	// is nil-free by construction: an absent binary means every page is read from the plain HTTP
	// response, exactly as before.
	Browser BrowserConfig `yaml:"browser"`
	// Assistant is the command assistant: how long its records live, how big a materialized set may
	// get, and how many rows one resolution may look at.
	Assistant AssistantConfig `yaml:"assistant"`
	Fetch     FetchConfig     `yaml:"fetch"`
	PDF       PDFConfig       `yaml:"pdf"`
	OCR       OCRConfig       `yaml:"ocr"`
	// Language holds the language of what the app WRITES: the interface's default, the claims the
	// distill step derives and the overviews the summary step writes. A source's own language is
	// detected per source and only tunes search; it never decides the output language.
	Language LanguageConfig `yaml:"language"`
}

// LanguageConfig is the content and interface language. Every value is an ISO 639-1 code (an ISO
// 639-3 code when no 639-1 exists); an empty value falls back to English. The UI language is only a
// DEFAULT: the PWA remembers the reader's own choice.
type LanguageConfig struct {
	UI      string `yaml:"ui"`
	Claims  string `yaml:"claims"`
	Summary string `yaml:"summary"`
}

type AppConfig struct {
	// Thought Keeper never launches a browser — you open the UI yourself (the
	// installed PWA, or the printed address). These are the desktop-shell options.
	Tray      bool `yaml:"tray"`
	Autostart bool `yaml:"autostart"`
	// SmartOpen makes the automatic open (tray "Open", the start-up open) prefer the INSTALLED PWA's
	// own launcher over the OS URL handler: the browser writes a launcher when it installs the app,
	// and starting that opens the app's own window (its icon, its window class) instead of a URL that
	// some handler has to place. Best-effort — when no launcher can be found the OS handler runs as
	// before. Off means: always the OS URL handler.
	SmartOpen bool `yaml:"smart_open"`
	// MemoryLimitMB is a SOFT ceiling on the Go heap (MiB); 0 means none. The association step
	// spikes the heap to read every claim and frees it again; without a ceiling the runtime returns
	// that memory to the OS only lazily, so the process can look big long after the work. A limit
	// makes the runtime collect sooner and give memory back on the way down — the knob for "the app
	// held 900 MB after a pass". Set it generously: below a pass's real working set it only buys GC
	// thrashing. It is a target, not a reservation: Go keeps the process below it where it can.
	MemoryLimitMB int `yaml:"memory_limit_mb"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
}

type VaultConfig struct {
	Root    string `yaml:"root"`
	Sources string `yaml:"sources"`
	Inbox   string `yaml:"inbox"`
	Atoms   string `yaml:"atoms"`
	// Wiki holds user-authored Markdown pages. A page is the truth; its source representation is
	// derived and lives under sources/ like every other source.
	Wiki     string `yaml:"wiki"`
	Clusters string `yaml:"clusters"`
	Asks     string `yaml:"asks"`
	// Commands holds the assistant's short-lived records: one utterance, the criteria it was
	// understood as, and the set it produced. They are pruned after `assistant.retention`, so the
	// directory is bounded by design.
	Commands string `yaml:"commands"`
	Trash    string `yaml:"trash"`
	// Vocabulary holds the canonical name vocabularies (topics, branches, tags) — the agent's
	// decisions about what a thing is CALLED, as opposed to what it says. A record, not a cache: a
	// reindex reads it, and it belongs in a versioned vault.
	Vocabulary string `yaml:"vocabulary"`
	// Interests holds the reader's standing rules: one Markdown file per interest. They are a
	// record like the vocabulary — a decision the index cannot rebuild — so they travel with the
	// vault and are never pruned.
	Interests string    `yaml:"interests"`
	Git       GitConfig `yaml:"git"`
}

type GitConfig struct {
	// IgnoreDerived keeps the DERIVED files out of a vault that is under version control: when the
	// vault is a git repository, the app makes sure .gitignore excludes them (append-only — a user's
	// own rules are never touched). On by default; turn it off to version the sidecars as well.
	IgnoreDerived bool `yaml:"ignore_derived"`
	// Enabled and CommitOnWrite are NOT implemented: the app never runs git. They are the shape of a
	// later feature (committing the vault on write), and they are documented as such so nobody
	// believes a setting does something it does not.
	Enabled       bool   `yaml:"enabled"`
	CommitOnWrite bool   `yaml:"commit_on_write"`
	Author        string `yaml:"author"`
}

type IndexConfig struct {
	// Path is the SQLite index. It sits in the VAULT ROOT: the index belongs to exactly one vault,
	// so moving or switching a vault takes it along — and it can be rebuilt from the vault's files
	// at any time (`reindex` + `rebuild-trees`).
	Path string `yaml:"path"`
	// Favicons is the one derived store that is NOT per source (two sources of one host share an
	// icon), so it lives in the vault's hidden cache directory.
	Favicons string `yaml:"favicons"`
}

type OpenAIConfig struct {
	BaseURL      string `yaml:"base_url"`
	APIKey       string `yaml:"api_key"`
	ModelCompile string `yaml:"model_compile"`
	// ReasoningEffortCompile keeps the cheap steps (distill, compile) from thinking for
	// thousands of tokens: those tokens are the wall-clock time. Empty = endpoint default.
	ReasoningEffortCompile string `yaml:"reasoning_effort_compile"`
	// ReasoningEffortDistill is separate on purpose: deciding what ONE claim is (distill) is the
	// only judgement call in the cheap phases — grouping and filing (compile) is mechanical. Empty
	// falls back to the compile value. Whether "low" earns its extra tokens is measurable: the
	// source log reports how many claims the AUDIT had to add afterwards, and what the parts cost.
	ReasoningEffortDistill string `yaml:"reasoning_effort_distill"`
	ReasoningEffortReason  string `yaml:"reasoning_effort_reason"`
	ModelReason            string `yaml:"model_reason"`
	MaxIterations          int    `yaml:"max_iterations"`
	// Retries and RetryBackoff make a slow endpoint transparent: a transient failure (timeout,
	// 429, 5xx) is retried with a doubling backoff instead of ending the run. A wrong key or a
	// bad request is not retried.
	Retries      int               `yaml:"retries"`
	RetryBackoff string            `yaml:"retry_backoff"`
	Timeout      string            `yaml:"timeout"`
	Headers      map[string]string `yaml:"headers"`
}

// PipelineConfig tunes the pipeline: the DOCUMENT steps (capture, outline, distill) run per source,
// the CLUSTER step runs over the whole vault while no document step runs. The knobs are grouped by
// that split.
type PipelineConfig struct {
	Auto        bool   `yaml:"auto"`
	AutoDistill bool   `yaml:"auto_distill"`
	Debounce    string `yaml:"debounce"`
	RunTimeout  string `yaml:"run_timeout"`
	// ClusterTimeout is the ceiling for one CLUSTER pass. The graph is built over the whole vault;
	// ten minutes cut it off before the association layer existed.
	ClusterTimeout string `yaml:"cluster_timeout"`
	// PartsPerRun bounds how many unclaimed PARTS (one text-bearing node each) one distill run
	// covers, so a large document is derived across several resumable runs.
	PartsPerRun int `yaml:"parts_per_run"`
	// AutoContinue queues the next run while a derivation is unfinished, so a book is
	// derived to the end without clicking again.
	AutoContinue bool `yaml:"auto_continue"`
	// ParallelRuns is how many document runs run at the same time — one per SOURCE (a source is never
	// derived twice at once). A source stuck against a slow endpoint must not hold up every other
	// document behind it.
	ParallelRuns int `yaml:"parallel_runs"`
	// ParallelQueries is how many Ask runs run at the same time. Ask is never gated, and one question
	// waiting for a slow model must not hold the next one: the reader can ask again while an answer
	// is still being written.
	ParallelQueries int `yaml:"parallel_queries"`
	// ParallelParts distills that many parts of a document at the same time. Parts are independent
	// (they only need the tree), so this divides a long document's wall-clock.
	ParallelParts int `yaml:"parallel_parts"`
	// ReconcileInterval is how often the consistency job walks the vault: it checks that what the
	// step records say is on disk (a deleted outline invalidates everything after it), and it asks
	// the cluster step for a pass whenever there is something for it to do. Empty or "0" turns it off.
	ReconcileInterval string `yaml:"reconcile_interval"`
	// ReconcileBatch is how many sources one consistency pass looks at (the passes walk the vault in
	// slices, so a huge vault is checked over several ticks).
	ReconcileBatch int `yaml:"reconcile_batch"`
}

type ClusterConfig struct {
	NeighborK          int     `yaml:"neighbor_k"`
	MinClaimScore      float64 `yaml:"min_claim_score"`
	MergeMinInternal   float64 `yaml:"merge_min_internal"`
	MergeMinGap        float64 `yaml:"merge_min_gap"`
	SplitMinCoreSize   int     `yaml:"split_min_core_size"`
	TopicSpanLimit     int     `yaml:"topic_span_limit"`
	MaxClusterClaims   int     `yaml:"max_cluster_claims"`
	AttachMinScore     float64 `yaml:"attach_min_score"`
	OverlapMinEdges    int     `yaml:"overlap_min_edges"`
	OverlapMax         int     `yaml:"overlap_max"`
	CrossTopicMinEdges int     `yaml:"cross_topic_min_edges"`
}

type CaptureConfig struct {
	DedupeByURL bool `yaml:"dedupe_by_url"`
	// RefreshInterval is the cadence at which ONE URL source (a page or a sitemap) may be revisited
	// (e.g. "168h" — once a week). The worker looks for due sources every hour; the interval itself
	// lives per source (url_checked_at), so a restart neither postpones nor doubles a refresh. Empty
	// disables the automatic refresh for pages and sitemaps (manual POST /api/v1/refresh-urls still
	// works). A blocked page is attempted again on the same cadence: it may become readable.
	RefreshInterval string `yaml:"refresh_interval"`
	// FeedRefreshInterval is the cadence for a FEED source (its item list), e.g. "24h". Feeds carry
	// new posts far more often than a page changes; empty disables the automatic feed refresh while
	// leaving pages and sitemaps on RefreshInterval.
	FeedRefreshInterval string `yaml:"feed_refresh_interval"`
	// MinTextChars: a fetched page with less extracted text than this is kept as a
	// bookmark (metadata only, never distilled) instead of an indexed document.
	MinTextChars int `yaml:"min_text_chars"`
	// ListingMaxPages bounds the INITIAL walk of a listing's pagination (a blog index, a newsletter
	// archive). A listing is captured by following every page it declares up to this many; a refresh
	// only reads page 1, because new articles appear there first. Default 100.
	ListingMaxPages int `yaml:"listing_max_pages"`
}

// AssistantConfig is the command assistant. A command is a short-lived record: the retention is
// what keeps the history small, and the set is bounded because a command is a UI state, not an
// export.
type AssistantConfig struct {
	// Retention is how long a command lives, e.g. "168h" (one week). Emptied, the history is kept
	// forever — and every address it ever handed out keeps working.
	Retention string `yaml:"retention"`
	// PruneInterval is how often the retention is ENFORCED, e.g. "24h". It is not a cadence that
	// needs to be fine: a command that lives an hour longer costs nothing, and the pass walks the
	// whole commands/ directory. Empty disables the automatic pass (the files stay).
	PruneInterval string `yaml:"prune_interval"`
	// MaxItems bounds the materialized set of one command. The banner says when it was cut.
	MaxItems int `yaml:"max_items"`
	// ScanLimit bounds how many rows one resolution looks at. Criteria the index cannot express (a
	// time window, a derivation state) are applied over that window, and the record says so.
	ScanLimit int `yaml:"scan_limit"`
}

type FetchConfig struct {
	Timeout      string `yaml:"timeout"`
	MaxBytes     int64  `yaml:"max_bytes"`
	UserAgent    string `yaml:"user_agent"`
	AllowPrivate bool   `yaml:"allow_private"`
}

// BrowserConfig is the local rendering engine. There is exactly one engine (Lightpanda); the
// section exists so its binary, its patience and its honesty are configurable, not because the
// engine is pluggable.
type BrowserConfig struct {
	// Path is the binary. Empty means "lightpanda from PATH". A missing binary is not an error:
	// the app reports it in Status and reads pages from the plain HTTP response.
	Path string `yaml:"path"`
	// Timeout is the ceiling for one rendered page: navigation, scripts and the dump.
	Timeout string `yaml:"timeout"`
	// Wait is how long a page may take after it loaded before the DOM is dumped.
	Wait string `yaml:"wait"`
	// WaitUntil is the event the dump waits for: `load` stops at the shell of a single-page app,
	// `networkidle` (the default) waits for the data its scripts fetched, `done` for full quiescence
	// (a page with constant background activity runs to `wait` instead).
	WaitUntil string `yaml:"wait_until"`
	// LoadStylesheets fetches CSS, so content hidden by a stylesheet is hidden in the DOM too. It
	// costs requests; images, iframes and workers stay off.
	LoadStylesheets bool `yaml:"load_stylesheets"`
	// ObeyRobots lets the browser honour the site's robots.txt. Off by default: the reader asked
	// for ONE page, and the app never crawls on its own.
	ObeyRobots bool `yaml:"obey_robots"`
}

type PDFConfig struct {
	TextTool string `yaml:"text_tool"`
	MaxBytes int64  `yaml:"max_bytes"`
	MaxPages int    `yaml:"max_pages"`
}

type OCRConfig struct {
	Enabled             bool     `yaml:"enabled"`
	Rasterizer          string   `yaml:"rasterizer"`
	Tesseract           string   `yaml:"tesseract"`
	Languages           []string `yaml:"languages"`
	DPI                 int      `yaml:"dpi"`
	MinTextCharsPerPage int      `yaml:"min_text_chars_per_page"`
	MaxPages            int      `yaml:"max_pages"`
	Timeout             string   `yaml:"timeout"`
}

// Defaults returns a fully populated configuration with sensible defaults.
func Defaults() Config {
	return Config{
		App:    AppConfig{Tray: true, Autostart: false, SmartOpen: true},
		Server: ServerConfig{Addr: "127.0.0.1:9210"},
		Vault: VaultConfig{
			Root:       "~/thought-keeper",
			Sources:    "sources",
			Inbox:      "inbox",
			Atoms:      "atoms",
			Wiki:       "wiki",
			Clusters:   "clusters",
			Asks:       "asks",
			Commands:   "commands",
			Trash:      ".trash",
			Vocabulary: "vocabulary",
			Git:        GitConfig{IgnoreDerived: true, CommitOnWrite: true, Author: "thought-keeper <agent@localhost>"},
		},
		Index: IndexConfig{Path: defaultIndexPath, Favicons: defaultFaviconsPath},
		OpenAI: OpenAIConfig{
			ModelCompile: "gpt-4o-mini",
			ModelReason:  "gpt-4o",
			// Minimal everywhere: this is mechanical work (grouping, filing, looking up).
			ReasoningEffortCompile: "minimal",
			ReasoningEffortDistill: "minimal",
			ReasoningEffortReason:  "minimal",
			MaxIterations:          16,
			// A compile turn with many atoms needs room; a timeout is retried, but a bigger
			// ceiling keeps it from happening in the first place.
			// A retry ladder on top of a 120 s ceiling is at most ~6 minutes for one call — bounded
			// by the run's own run_timeout, and never a silent twenty-minute hang.
			Retries:      2,
			RetryBackoff: "2s",
			// The design doc has always said 300 s: one call may be a whole-catalog vocabulary question
			// or a compile turn over a batch of claims, and 120 s cut both of them off. A timeout is
			// retried, so the ceiling is patience, not a budget.
			Timeout: "300s",
		},
		Pipeline: PipelineConfig{
			Auto: true, AutoDistill: true, Debounce: "3s", RunTimeout: "10m", ClusterTimeout: "30m",
			PartsPerRun: 12, AutoContinue: true, ParallelParts: 4, ParallelRuns: 4, ParallelQueries: 3,
			ReconcileInterval: "1m", ReconcileBatch: 500,
		},
		Cluster: ClusterConfig{
			NeighborK: 16, MinClaimScore: 0.05, MergeMinInternal: 0.22, MergeMinGap: 0.04,
			SplitMinCoreSize: 3, TopicSpanLimit: 12, MaxClusterClaims: 80,
			AttachMinScore: 0.18, OverlapMinEdges: 4, OverlapMax: 2, CrossTopicMinEdges: 3,
		},
		Capture: CaptureConfig{
			DedupeByURL: true, RefreshInterval: "168h", FeedRefreshInterval: "24h",
			MinTextChars: 400, ListingMaxPages: 100,
		},
		Assistant: AssistantConfig{
			// A week is long enough to find yesterday's request again, and short enough that the
			// history never becomes a second vault.
			Retention: "168h", PruneInterval: "24h", MaxItems: 2000, ScanLimit: 1000,
		},
		Fetch: FetchConfig{
			Timeout:   "20s",
			MaxBytes:  5 << 20,
			UserAgent: "thought-keeper/0.1 (+local)",
		},
		Browser: BrowserConfig{
			Timeout: "30s", Wait: "5s", WaitUntil: "networkidle", LoadStylesheets: true,
		},
		PDF: PDFConfig{TextTool: "pdftotext", MaxBytes: 50 << 20, MaxPages: 2000},
		OCR: OCRConfig{
			Enabled:             true,
			Rasterizer:          "pdftocairo",
			Tesseract:           "tesseract",
			Languages:           []string{"eng"},
			DPI:                 300,
			MinTextCharsPerPage: 32,
			MaxPages:            500,
			Timeout:             "5m",
		},
		Language: LanguageConfig{UI: "en", Claims: "en", Summary: "en"},
	}
}

// Load reads the config from path. An empty path uses the default location. A
// missing file is not an error — defaults are used. ${ENV} references are expanded
// and a leading ~ is resolved to the home directory.
func Load(path string) (Config, error) {
	loaded, err := LoadFull(path)
	return loaded.Config, err
}

// LoadFull is Load with the provenance the Config overlay needs: the resolved path, whether the file
// exists, which keys the FILE declared, which keys the VAULT file declared, and which keys the
// ENVIRONMENT decided (with the variable that decided them). The values are exactly what Load
// returns — Load is a wrapper around this.
//
// The effective configuration is the machine file over the defaults, then the vault's own
// vault.yaml over that. A vault key the vault file declares wins over the machine; a key it leaves
// out falls back to the machine, so an existing install without vault.yaml keeps working.
func LoadFull(path string) (Loaded, error) {
	if path == "" {
		path = DefaultPath()
	}
	resolved := expandHome(path)
	data, readErr := os.ReadFile(resolved)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		cfg := Defaults()
		env := applyDefaults(&cfg)
		return Loaded{Config: cfg, Path: resolved, Env: env, VaultPath: VaultPath(cfg)},
			fmt.Errorf("read config: %w", readErr)
	}
	text := ""
	if exists {
		text = os.ExpandEnv(string(data))
	}
	cfg, err := machineConfig(data)
	if err != nil {
		env := applyDefaults(&cfg)
		return Loaded{Config: cfg, Path: resolved, Exists: exists, Env: env, VaultPath: VaultPath(cfg)},
			fmt.Errorf("parse config: %w", err)
	}
	vaultPath := VaultPath(cfg)
	vaultData, vaultErr := os.ReadFile(vaultPath)
	vaultExists := vaultErr == nil
	if vaultErr != nil && !os.IsNotExist(vaultErr) {
		env := applyDefaults(&cfg)
		return Loaded{
			Config: cfg, Path: resolved, Exists: exists, Set: declaredKeys(text),
			Env: env, VaultPath: vaultPath,
		}, fmt.Errorf("read vault config: %w", vaultErr)
	}
	vaultSet := map[string]bool{}
	if vaultExists {
		merged := cfg
		vaultSet, err = mergeVault(&merged, vaultData)
		if err != nil {
			// The overlay still shows the running values; the file is what is broken.
			env := applyDefaults(&cfg)
			return Loaded{
				Config: cfg, Path: resolved, Exists: exists, Set: declaredKeys(text),
				Env: env, VaultPath: vaultPath, VaultExists: true,
			}, err
		}
		cfg = merged
	}
	env := applyDefaults(&cfg)
	return Loaded{
		Config:      cfg,
		Path:        resolved,
		Exists:      exists,
		Set:         declaredKeys(text),
		Env:         env,
		VaultPath:   vaultPath,
		VaultExists: vaultExists,
		VaultSet:    vaultSet,
	}, nil
}

// applyDefaults fills empty fields that the YAML left zeroed. The returned map names the dotted keys
// the ENVIRONMENT decided, with the variable that decided each one, so a reader can be told that a
// value in the overlay is not the file's to change.
func applyDefaults(c *Config) map[string]string {
	env := map[string]string{}
	d := Defaults()
	if c.Server.Addr == "" {
		c.Server.Addr = d.Server.Addr
	}
	if c.Vault.Root == "" {
		c.Vault.Root = d.Vault.Root
	}
	if c.Vault.Sources == "" {
		c.Vault.Sources = d.Vault.Sources
	}
	if c.Vault.Inbox == "" {
		c.Vault.Inbox = d.Vault.Inbox
	}
	if c.Vault.Atoms == "" {
		c.Vault.Atoms = d.Vault.Atoms
	}
	if c.Vault.Wiki == "" {
		c.Vault.Wiki = d.Vault.Wiki
	}
	if c.Vault.Clusters == "" {
		c.Vault.Clusters = d.Vault.Clusters
	}
	if c.Vault.Trash == "" {
		c.Vault.Trash = d.Vault.Trash
	}
	if c.Index.Path == "" {
		c.Index.Path = d.Index.Path
	}
	if c.OpenAI.Timeout == "" {
		c.OpenAI.Timeout = d.OpenAI.Timeout
	}
	if c.OpenAI.ReasoningEffortDistill == "" {
		c.OpenAI.ReasoningEffortDistill = c.OpenAI.ReasoningEffortCompile
	}
	if c.OpenAI.Retries == 0 {
		c.OpenAI.Retries = d.OpenAI.Retries
	}
	if c.OpenAI.RetryBackoff == "" {
		c.OpenAI.RetryBackoff = d.OpenAI.RetryBackoff
	}
	if c.Pipeline.PartsPerRun <= 0 {
		c.Pipeline.PartsPerRun = d.Pipeline.PartsPerRun
	}
	if c.Pipeline.ReconcileBatch <= 0 {
		c.Pipeline.ReconcileBatch = d.Pipeline.ReconcileBatch
	}
	if c.Capture.ListingMaxPages <= 0 {
		c.Capture.ListingMaxPages = d.Capture.ListingMaxPages
	}
	if c.Pipeline.ParallelRuns <= 0 {
		c.Pipeline.ParallelRuns = d.Pipeline.ParallelRuns
	}
	if c.Pipeline.ParallelQueries <= 0 {
		c.Pipeline.ParallelQueries = d.Pipeline.ParallelQueries
	}
	if c.Pipeline.ParallelParts <= 0 {
		c.Pipeline.ParallelParts = d.Pipeline.ParallelParts
	}
	if c.Pipeline.RunTimeout == "" {
		c.Pipeline.RunTimeout = d.Pipeline.RunTimeout
	}
	if c.Pipeline.ClusterTimeout == "" {
		c.Pipeline.ClusterTimeout = d.Pipeline.ClusterTimeout
	}
	if c.Cluster.NeighborK <= 0 {
		c.Cluster.NeighborK = d.Cluster.NeighborK
	}
	if c.Cluster.MinClaimScore <= 0 {
		c.Cluster.MinClaimScore = d.Cluster.MinClaimScore
	}
	if c.Cluster.MergeMinInternal <= 0 {
		c.Cluster.MergeMinInternal = d.Cluster.MergeMinInternal
	}
	if c.Cluster.MergeMinGap <= 0 {
		c.Cluster.MergeMinGap = d.Cluster.MergeMinGap
	}
	if c.Cluster.SplitMinCoreSize <= 0 {
		c.Cluster.SplitMinCoreSize = d.Cluster.SplitMinCoreSize
	}
	if c.Cluster.TopicSpanLimit <= 0 {
		c.Cluster.TopicSpanLimit = d.Cluster.TopicSpanLimit
	}
	if c.Cluster.MaxClusterClaims <= 0 {
		c.Cluster.MaxClusterClaims = d.Cluster.MaxClusterClaims
	}
	if c.Cluster.AttachMinScore <= 0 {
		c.Cluster.AttachMinScore = d.Cluster.AttachMinScore
	}
	if c.Cluster.OverlapMinEdges <= 0 {
		c.Cluster.OverlapMinEdges = d.Cluster.OverlapMinEdges
	}
	if c.Cluster.OverlapMax <= 0 {
		c.Cluster.OverlapMax = d.Cluster.OverlapMax
	}
	if c.Cluster.CrossTopicMinEdges <= 0 {
		c.Cluster.CrossTopicMinEdges = d.Cluster.CrossTopicMinEdges
	}
	if c.PDF.TextTool == "" {
		c.PDF.TextTool = d.PDF.TextTool
	}
	if c.Browser.Timeout == "" {
		c.Browser.Timeout = d.Browser.Timeout
	}
	if c.Browser.Wait == "" {
		c.Browser.Wait = d.Browser.Wait
	}
	if c.Browser.WaitUntil == "" {
		c.Browser.WaitUntil = d.Browser.WaitUntil
	}
	if c.OCR.Rasterizer == "" {
		c.OCR.Rasterizer = d.OCR.Rasterizer
	}
	if c.OCR.Tesseract == "" {
		c.OCR.Tesseract = d.OCR.Tesseract
	}
	if len(c.OCR.Languages) == 0 {
		c.OCR.Languages = d.OCR.Languages
	}
	c.Language.UI = normalizeLanguage(c.Language.UI, d.Language.UI)
	c.Language.Claims = normalizeLanguage(c.Language.Claims, d.Language.Claims)
	c.Language.Summary = normalizeLanguage(c.Language.Summary, d.Language.Summary)
	// The OpenAI key/base URL are read from the environment even when the config
	// file has no `openai:` block, so OPENAI_API_KEY just works.
	if c.OpenAI.APIKey == "" {
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			c.OpenAI.APIKey, env["openai.api_key"] = v, "OPENAI_API_KEY"
		}
	}
	if c.OpenAI.BaseURL == "" {
		if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
			c.OpenAI.BaseURL, env["openai.base_url"] = v, "OPENAI_BASE_URL"
		}
	}
	// Models resolve env → yaml → default, so a shared endpoint (e.g. opencode Zen,
	// which does not serve gpt-4o-mini) is one env var away.
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		c.OpenAI.ModelCompile = v
		c.OpenAI.ModelReason = v
		env["openai.model_compile"], env["openai.model_reason"] = "OPENAI_MODEL", "OPENAI_MODEL"
	}
	if v := os.Getenv("OPENAI_MODEL_COMPILE"); v != "" {
		c.OpenAI.ModelCompile, env["openai.model_compile"] = v, "OPENAI_MODEL_COMPILE"
	}
	if v := os.Getenv("OPENAI_MODEL_REASON"); v != "" {
		c.OpenAI.ModelReason, env["openai.model_reason"] = v, "OPENAI_MODEL_REASON"
	}
	if v := os.Getenv("OPENAI_REASONING_EFFORT_COMPILE"); v != "" {
		c.OpenAI.ReasoningEffortCompile, env["openai.reasoning_effort_compile"] = v, "OPENAI_REASONING_EFFORT_COMPILE"
	}
	if v := os.Getenv("OPENAI_REASONING_EFFORT_REASON"); v != "" {
		c.OpenAI.ReasoningEffortReason, env["openai.reasoning_effort_reason"] = v, "OPENAI_REASONING_EFFORT_REASON"
	}
	// The distill step is its own knob: deciding what ONE claim is is the only judgement call in
	// the mechanical phases, so it is the one worth raising to "low".
	if v := os.Getenv("OPENAI_REASONING_EFFORT_DISTILL"); v != "" {
		c.OpenAI.ReasoningEffortDistill, env["openai.reasoning_effort_distill"] = v, "OPENAI_REASONING_EFFORT_DISTILL"
	}
	// Deployment knobs: how patient the caller is with a shared endpoint. They are reachable from
	// the environment for the same reason the models are — a shared endpoint is often one env var
	// away from working, and a dev's .env is where that lives.
	if v := os.Getenv("OPENAI_TIMEOUT"); v != "" {
		c.OpenAI.Timeout, env["openai.timeout"] = v, "OPENAI_TIMEOUT"
	}
	if v := os.Getenv("OPENAI_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.OpenAI.Retries, env["openai.retries"] = n, "OPENAI_RETRIES"
		}
	}
	if v := os.Getenv("OPENAI_RETRY_BACKOFF"); v != "" {
		c.OpenAI.RetryBackoff, env["openai.retry_backoff"] = v, "OPENAI_RETRY_BACKOFF"
	}
	return env
}

// normalizeLanguage trims and lower-cases one language code, falling back to the default when the
// value is empty. A code the app does not know is still accepted: it is handed to the model as a
// language name hint, and a wrong guess is corrected in the config rather than silently ignored.
func normalizeLanguage(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

// VaultRoot returns the absolute vault root.
func (c Config) VaultRoot() string { return expandHome(c.Vault.Root) }

// SourcesDir returns the absolute sources directory.
func (c Config) SourcesDir() string { return filepath.Join(c.VaultRoot(), c.Vault.Sources) }

// InboxDir returns the absolute drop zone directory.
func (c Config) InboxDir() string { return filepath.Join(c.VaultRoot(), c.Vault.Inbox) }

// AtomsDir returns the absolute atoms directory.
func (c Config) AtomsDir() string { return filepath.Join(c.VaultRoot(), c.Vault.Atoms) }

// WikiDir returns the absolute wiki-page directory.
func (c Config) WikiDir() string { return filepath.Join(c.VaultRoot(), c.Vault.Wiki) }

// ClustersDir returns the absolute association-layer directory.
func (c Config) ClustersDir() string { return filepath.Join(c.VaultRoot(), c.Vault.Clusters) }

// AsksDir returns the absolute saved-query directory. It is a record area, not a cache: the Ask
// view's questions and answers live in the vault.
func (c Config) AsksDir() string {
	dir := strings.TrimSpace(c.Vault.Asks)
	if dir == "" {
		dir = "asks"
	}
	return filepath.Join(c.VaultRoot(), dir)
}

// CommandsDir returns the absolute assistant directory. It is a record area with an expiry: the
// consistency job moves commands older than `assistant.retention` to the trash.
func (c Config) CommandsDir() string {
	dir := strings.TrimSpace(c.Vault.Commands)
	if dir == "" {
		dir = "commands"
	}
	return filepath.Join(c.VaultRoot(), dir)
}

// VocabularyDir returns the absolute vocabulary directory: one file per namespace, holding the
// canonical names and what they absorbed.
func (c Config) VocabularyDir() string {
	dir := strings.TrimSpace(c.Vault.Vocabulary)
	if dir == "" {
		dir = "vocabulary"
	}
	return filepath.Join(c.VaultRoot(), dir)
}

// InterestsDir returns the absolute interests directory: one Markdown file per standing rule.
func (c Config) InterestsDir() string {
	dir := strings.TrimSpace(c.Vault.Interests)
	if dir == "" {
		dir = "interests"
	}
	return filepath.Join(c.VaultRoot(), dir)
}

// derivedDir is the vault's hidden app-state directory. It holds what is not about one single
// source: the favicons (two sources of one host share an icon) and the reader's seen watermark,
// which is a record rather than a cache but is likewise no knowledge of the vault.
//
// Everything that belongs to ONE source lives next to that source instead
// (sources/<ab>/<cd>/<hash>.md · .meta · .outline · .log), and the index database sits in the
// vault root. So a source is one folder entry with its whole story, deleting it deletes the story,
// and the vault travels — or is switched — as a whole.
func (c Config) derivedDir() string {
	return filepath.Join(c.VaultRoot(), ".thought-keeper")
}

// IndexDB returns the absolute index database path: the VAULT ROOT (index.path is relative to it).
// An absolute path is honoured, so a setup from before this decision keeps its location.
func (c Config) IndexDB() string {
	p := strings.TrimSpace(c.Index.Path)
	if p == "" {
		return filepath.Join(c.VaultRoot(), "index.db")
	}
	if strings.HasPrefix(p, "~") || filepath.IsAbs(p) {
		return expandHome(p)
	}
	return filepath.Join(c.VaultRoot(), p)
}

// ReaderDir returns the absolute reader-state directory: the shared seen watermark lives here,
// hidden with the app's other state. It is a RECORD, not a cache: reindex never touches it.
func (c Config) ReaderDir() string {
	return filepath.Join(c.derivedDir(), "reader")
}

// FaviconsDir returns the absolute favicon store directory (the vault's hidden cache).
func (c Config) FaviconsDir() string {
	p := strings.TrimSpace(c.Index.Favicons)
	if p == "" {
		return filepath.Join(c.derivedDir(), "favicons")
	}
	if strings.HasPrefix(p, "~") || filepath.IsAbs(p) {
		return expandHome(p)
	}
	return filepath.Join(c.derivedDir(), p)
}

// DefaultPath returns $XDG_CONFIG_HOME/thought-keeper/config.yaml.
func DefaultPath() string {
	return filepath.Join(configDir(), "thought-keeper", "config.yaml")
}

// RuntimeDir returns $XDG_RUNTIME_DIR/thought-keeper (falling back to the temp dir).
func RuntimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "thought-keeper")
	}
	return filepath.Join(os.TempDir(), "thought-keeper")
}

// EndpointPath returns the runtime file that publishes the running instance address.
func EndpointPath() string { return filepath.Join(RuntimeDir(), "endpoint") }

func configDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir() //nolint:errcheck // best effort; the caller handles the empty result
	return filepath.Join(home, ".config")
}

// The derived data lives in the vault. These defaults are RELATIVE on purpose: an unconfigured setup
// keeps its index in the vault root and its cache beside it, so moving or switching a vault moves
// everything that belongs to it. config.example.yaml shows the same.
const (
	defaultIndexPath    = "index.db"
	defaultFaviconsPath = "favicons"
)

// expandHome resolves a leading ~ to the user's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// RunTimeoutDuration is the hard ceiling of one run (document or cluster pass) as a duration.
func (c PipelineConfig) RunTimeoutDuration() time.Duration {
	d, err := time.ParseDuration(c.RunTimeout)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}
