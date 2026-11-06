package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Errors the Config writer reports. The HTTP layer maps ErrNotWritable to 400 and ErrInvalidValue
// to 422; a parse or I/O failure is a 500 because the file, not the request, is the problem.
var (
	ErrNotWritable  = errors.New("configuration key is not writable")
	ErrInvalidValue = errors.New("invalid configuration value")
)

// writableKeys is the curated Tier 1 allowlist. A knob is here when the running process can apply
// it without a new vault, index or listener. Everything else is read-only in the overlay, even where
// the writer could technically reach it: a vault path is a session, not a setting.
var writableKeys = map[string]bool{
	"language.ui":                     true,
	"language.claims":                 true,
	"language.summary":                true,
	"openai.model_compile":            true,
	"openai.model_reason":             true,
	"openai.reasoning_effort_compile": true,
	"openai.reasoning_effort_distill": true,
	"openai.reasoning_effort_reason":  true,
	"pipeline.auto":                   true,
	"pipeline.auto_distill":           true,
	"pipeline.debounce":               true,
	"pipeline.parallel_runs":          true,
	"pipeline.parallel_queries":       true,
	"pipeline.parallel_parts":         true,
	"pipeline.parts_per_run":          true,
	"capture.refresh_interval":        true,
	"capture.feed_refresh_interval":   true,
	"browser.path":                    true,
	"ocr.enabled":                     true,
	"ocr.languages":                   true,
	"app.memory_limit_mb":             true,
}

// Writable reports whether the Config overlay may write this key.
func Writable(key string) bool { return writableKeys[key] }

// Validate checks the values the app can act on. It is deliberately lenient about things the app
// already tolerates (an empty interval means "disabled", a path may not exist yet) and strict about
// values that would make a run misbehave (a zero worker count, a duration that is not a duration, a
// score outside [0,1], a language that is not a code).
//
// It is called on the WHOLE config after a patch, not only on the patched keys: a file that was
// already broken must not become the running config behind the writer.
func Validate(c Config) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(c.Server.Addr) == "" {
		add("server.addr: empty")
	} else if _, port, err := net.SplitHostPort(c.Server.Addr); err != nil {
		add("server.addr: %v", err)
	} else if _, err := strconv.Atoi(port); err != nil {
		add("server.addr: port %q is not a number", port)
	}

	// An empty duration means "disabled" for the optional ones; when it is set, it must parse.
	for key, raw := range map[string]string{
		"pipeline.debounce":             c.Pipeline.Debounce,
		"pipeline.run_timeout":          c.Pipeline.RunTimeout,
		"pipeline.cluster_timeout":      c.Pipeline.ClusterTimeout,
		"pipeline.reconcile_interval":   c.Pipeline.ReconcileInterval,
		"capture.refresh_interval":      c.Capture.RefreshInterval,
		"capture.feed_refresh_interval": c.Capture.FeedRefreshInterval,
		"assistant.retention":           c.Assistant.Retention,
		"assistant.prune_interval":      c.Assistant.PruneInterval,
		"openai.timeout":                c.OpenAI.Timeout,
		"openai.retry_backoff":          c.OpenAI.RetryBackoff,
		"browser.timeout":               c.Browser.Timeout,
		"browser.wait":                  c.Browser.Wait,
		"ocr.timeout":                   c.OCR.Timeout,
		"fetch.timeout":                 c.Fetch.Timeout,
	} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			add("%s: %v", key, err)
			continue
		}
		if d < 0 {
			add("%s: must not be negative", key)
		}
	}

	for key, value := range map[string]int{
		"pipeline.parts_per_run":      c.Pipeline.PartsPerRun,
		"pipeline.parallel_runs":      c.Pipeline.ParallelRuns,
		"pipeline.parallel_queries":   c.Pipeline.ParallelQueries,
		"pipeline.parallel_parts":     c.Pipeline.ParallelParts,
		"pipeline.reconcile_batch":    c.Pipeline.ReconcileBatch,
		"cluster.neighbor_k":          c.Cluster.NeighborK,
		"cluster.split_min_core_size": c.Cluster.SplitMinCoreSize,
		"cluster.topic_span_limit":    c.Cluster.TopicSpanLimit,
		"cluster.max_cluster_claims":  c.Cluster.MaxClusterClaims,
		"ocr.dpi":                     c.OCR.DPI,
	} {
		if value <= 0 {
			add("%s: must be greater than zero", key)
		}
	}
	for key, value := range map[string]int{
		"app.memory_limit_mb":           c.App.MemoryLimitMB,
		"capture.min_text_chars":        c.Capture.MinTextChars,
		"capture.listing_max_pages":     c.Capture.ListingMaxPages,
		"pdf.max_pages":                 c.PDF.MaxPages,
		"ocr.max_pages":                 c.OCR.MaxPages,
		"ocr.min_text_chars_per_page":   c.OCR.MinTextCharsPerPage,
		"cluster.overlap_min_edges":     c.Cluster.OverlapMinEdges,
		"cluster.overlap_max":           c.Cluster.OverlapMax,
		"cluster.cross_topic_min_edges": c.Cluster.CrossTopicMinEdges,
	} {
		if value < 0 {
			add("%s: must not be negative", key)
		}
	}
	for key, value := range map[string]float64{
		"cluster.min_claim_score":    c.Cluster.MinClaimScore,
		"cluster.merge_min_internal": c.Cluster.MergeMinInternal,
		"cluster.merge_min_gap":      c.Cluster.MergeMinGap,
		"cluster.attach_min_score":   c.Cluster.AttachMinScore,
	} {
		if value < 0 || value > 1 {
			add("%s: %v is outside [0,1]", key, value)
		}
	}

	switch c.Browser.WaitUntil {
	case "", "load", "networkidle", "done":
	default:
		add("browser.wait_until: %q is not load, networkidle or done", c.Browser.WaitUntil)
	}

	for key, value := range map[string]string{
		"language.ui":      c.Language.UI,
		"language.claims":  c.Language.Claims,
		"language.summary": c.Language.Summary,
	} {
		if !isLanguageCode(value) {
			add("%s: %q is not a language code", key, value)
		}
	}
	if strings.TrimSpace(c.OpenAI.ModelCompile) == "" {
		add("openai.model_compile: empty")
	}
	if strings.TrimSpace(c.OpenAI.ModelReason) == "" {
		add("openai.model_reason: empty")
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidValue, strings.Join(problems, "; "))
	}
	return nil
}

// isLanguageCode accepts an ISO 639-1 or 639-3 code: two or three ASCII letters, already normalized
// to lower case by applyDefaults.
func isLanguageCode(value string) bool {
	if len(value) < 2 || len(value) > 3 {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
