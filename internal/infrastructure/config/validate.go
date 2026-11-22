package config

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/modelwindow"
)

// Errors the Config writer reports. The HTTP layer maps ErrNotWritable to 400 and ErrInvalidValue
// to 422; a parse or I/O failure is a 500 because the file, not the request, is the problem.
var (
	ErrNotWritable  = errors.New("configuration key is not writable")
	ErrInvalidValue = errors.New("invalid configuration value")
)

// The two editable groups the Config overlay shows. A knob is writable when the running process can
// apply it without a new vault, index or listener; everything else is read-only in the overlay, even
// where the writer could technically reach it: a vault path is a session, not a setting.
//
// TierBasic is the curated set a reader changes for a reason. TierAdvanced is the expert surface,
// behind a reveal: timeouts, the cluster arithmetic, capture and fetch limits, the tool paths. Both
// tiers are applied the same way — the tier only decides what the overlay shows unasked.
const (
	TierBasic    = "basic"
	TierAdvanced = "advanced"
)

var writableKeys = map[string]string{
	"language.ui":                     TierBasic,
	"language.claims":                 TierBasic,
	"language.summary":                TierBasic,
	"openai.model_compile":            TierBasic,
	"openai.model_reason":             TierBasic,
	"openai.reasoning_effort_compile": TierBasic,
	"openai.reasoning_effort_distill": TierBasic,
	"openai.reasoning_effort_reason":  TierBasic,
	"openai.blocked_windows":          TierBasic,
	"pipeline.auto":                   TierBasic,
	"pipeline.auto_distill":           TierBasic,
	"pipeline.debounce":               TierBasic,
	"pipeline.parallel_runs":          TierBasic,
	"pipeline.parallel_queries":       TierBasic,
	"pipeline.parallel_parts":         TierBasic,
	"pipeline.parts_per_run":          TierBasic,
	"capture.refresh_interval":        TierBasic,
	"capture.feed_refresh_interval":   TierBasic,
	"capture.refresh_min_gap":         TierBasic,
	"browser.path":                    TierBasic,
	"ocr.enabled":                     TierBasic,
	"ocr.languages":                   TierBasic,
	"app.memory_limit_mb":             TierBasic,

	// Vocabulary: the automatic coarsening. "On" is housekeeping, the budget is how long one pass
	// may hold the gate before it hands it back. The three ceilings are vault-scoped settings a
	// reader retunes when a shelf is too coarse or too fine for the way they browse; a target that
	// moves down trims the shelf on the next look.
	"vocabulary.topics.max_names":      TierBasic,
	"vocabulary.source_tags.max_names": TierBasic,
	"vocabulary.wiki_tags.max_names":   TierBasic,
	"vocabulary.coarsen.automatic":     TierBasic,
	"vocabulary.retopic.automatic":     TierBasic,
	"vocabulary.coarsen.budget":        TierAdvanced,
	"vocabulary.retopic.budget":        TierAdvanced,
	"vocabulary.shelf.refit_interval":  TierAdvanced,
	"vocabulary.shelf.max_share":       TierAdvanced,
	"vocabulary.shelf.min_share":       TierAdvanced,

	// Agent: patience with one endpoint, not a different endpoint.
	"openai.timeout":        TierAdvanced,
	"openai.retries":        TierAdvanced,
	"openai.retry_backoff":  TierAdvanced,
	"openai.max_iterations": TierAdvanced,
	"openai.max_concurrent": TierAdvanced,

	// Pipeline: ceilings and background cadences.
	"pipeline.run_timeout":        TierAdvanced,
	"pipeline.cluster_timeout":    TierAdvanced,
	"pipeline.auto_continue":      TierAdvanced,
	"pipeline.reconcile_interval": TierAdvanced,
	"pipeline.reconcile_batch":    TierAdvanced,
	"pipeline.audit_every":        TierAdvanced,

	// Capture: what counts as a readable page, how far a listing is walked, and what counts as a
	// change worth telling the reader about.
	"capture.min_text_chars":         TierAdvanced,
	"capture.listing_max_pages":      TierAdvanced,
	"capture.change_min_words":       TierAdvanced,
	"capture.change_min_word_length": TierAdvanced,
	"capture.change_min_percent":     TierAdvanced,

	// Cluster: step 4's arithmetic. A change applies to the next pass, never to a running one.
	"cluster.neighbor_k":            TierAdvanced,
	"cluster.min_claim_score":       TierAdvanced,
	"cluster.merge_min_internal":    TierAdvanced,
	"cluster.merge_min_gap":         TierAdvanced,
	"cluster.split_min_core_size":   TierAdvanced,
	"cluster.topic_span_limit":      TierAdvanced,
	"cluster.max_cluster_claims":    TierAdvanced,
	"cluster.attach_min_score":      TierAdvanced,
	"cluster.overlap_min_edges":     TierAdvanced,
	"cluster.overlap_max":           TierAdvanced,
	"cluster.cross_topic_min_edges": TierAdvanced,

	// Assistant: how long a command record lives and how large one result may grow.
	"assistant.retention":      TierAdvanced,
	"assistant.prune_interval": TierAdvanced,
	"assistant.max_items":      TierAdvanced,
	"assistant.scan_limit":     TierAdvanced,

	// Fetch and browser: the limits of reading one page.
	"fetch.timeout":    TierAdvanced,
	"fetch.max_bytes":  TierAdvanced,
	"fetch.user_agent": TierAdvanced,

	"browser.timeout":          TierAdvanced,
	"browser.wait":             TierAdvanced,
	"browser.wait_until":       TierAdvanced,
	"browser.load_stylesheets": TierAdvanced,
	"browser.obey_robots":      TierAdvanced,

	// PDF and OCR: the local text path and its tools.
	"pdf.text_tool": TierAdvanced,
	"pdf.max_bytes": TierAdvanced,
	"pdf.max_pages": TierAdvanced,

	"ocr.rasterizer":              TierAdvanced,
	"ocr.tesseract":               TierAdvanced,
	"ocr.dpi":                     TierAdvanced,
	"ocr.min_text_chars_per_page": TierAdvanced,
	"ocr.max_pages":               TierAdvanced,
	"ocr.timeout":                 TierAdvanced,

	"app.smart_open": TierAdvanced,
}

// Tier names the overlay group of a writable key: TierBasic, TierAdvanced, or "" for a read-only
// knob.
func Tier(key string) string { return writableKeys[key] }

// Writable reports whether the Config overlay may write this key (either tier).
func Writable(key string) bool { return writableKeys[key] != "" }

// writableKeyList returns the allowlisted keys in a stable order.
func writableKeyList() []string {
	keys := make([]string, 0, len(writableKeys))
	for key := range writableKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Validate checks the values the app can act on. It is deliberately lenient about things the app
// already tolerates (a cadence may be empty, meaning "off") and strict about values that would make a
// run misbehave (a zero worker count, a duration that is not a duration, a score outside [0,1], a
// language that is not a code).
//
// It is called on the WHOLE config after a patch, not only on the patched keys: a file that was
// already broken must not become the running config behind the writer.
func Validate(c Config) error {
	var problems []string
	if problem := addrProblem(c.Server.Addr); problem != "" {
		problems = append(problems, problem)
	}
	values := valuesOf(c)
	for _, key := range writableKeyList() {
		if problem := keyProblem(key, values[key]); problem != "" {
			problems = append(problems, problem)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidValue, strings.Join(problems, "; "))
	}
	return nil
}

// valuesOf reads every knob's effective value off the config. It reuses the same YAML walk the
// overlay uses, so a new knob is validated without a second table that could drift from the struct.
func valuesOf(c Config) map[string]any {
	out := map[string]any{}
	for _, section := range sectionsOf(c) {
		for _, field := range section.Fields {
			out[field.Key] = field.Value
		}
	}
	return out
}

func addrProblem(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return "server.addr: empty"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("server.addr: %v", err)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Sprintf("server.addr: port %q is not a number", port)
	}
	return ""
}

var (
	reasoningEfforts = []string{"minimal", "low", "medium", "high"}
	waitUntilValues  = []string{"load", "domcontentloaded", "networkalmostidle", "networkidle", "done"}
)

// durationRules says which durations are usable. allowEmpty means "empty = off" (the app skips the
// cadence or the timeout); positive means a zero would be silently replaced by a default at run time,
// so the writer refuses it instead of writing a value the app will not use.
var durationRules = map[string]struct {
	allowEmpty bool
	positive   bool
}{
	"pipeline.debounce":               {allowEmpty: true},
	"capture.refresh_interval":        {allowEmpty: true},
	"capture.feed_refresh_interval":   {allowEmpty: true},
	"capture.refresh_min_gap":         {allowEmpty: true},
	"pipeline.reconcile_interval":     {allowEmpty: true},
	"assistant.prune_interval":        {allowEmpty: true},
	"ocr.timeout":                     {allowEmpty: true},
	"vocabulary.coarsen.budget":       {allowEmpty: true},
	"vocabulary.retopic.budget":       {allowEmpty: true},
	"vocabulary.shelf.refit_interval": {allowEmpty: true},
	"pipeline.run_timeout":            {},
	"pipeline.cluster_timeout":        {},
	"openai.retry_backoff":            {},
	"openai.timeout":                  {positive: true},
	"assistant.retention":             {positive: true},
	"browser.timeout":                 {positive: true},
	"browser.wait":                    {positive: true},
	"fetch.timeout":                   {positive: true},
}

// intRules is the smallest acceptable value of an integer knob. A minimum of 1 is used wherever the
// app substitutes a default for <= 0: the file must say what the process does.
var intRules = map[string]int64{
	"pipeline.parts_per_run":        1,
	"pipeline.parallel_runs":        1,
	"pipeline.parallel_queries":     1,
	"pipeline.parallel_parts":       1,
	"pipeline.reconcile_batch":      1,
	"openai.max_iterations":         1,
	"openai.max_concurrent":         1,
	"pipeline.audit_every":          1,
	"openai.retries":                1,
	"cluster.neighbor_k":            1,
	"cluster.split_min_core_size":   1,
	"cluster.topic_span_limit":      1,
	"cluster.max_cluster_claims":    1,
	"cluster.overlap_min_edges":     1,
	"cluster.overlap_max":           1,
	"cluster.cross_topic_min_edges": 1,
	"assistant.max_items":           1,
	"assistant.scan_limit":          1,
	"capture.min_text_chars":        1,
	"capture.listing_max_pages":     1,
	// Zero is meaningful: the rule is off (any difference is a material change).
	"capture.change_min_words":       0,
	"capture.change_min_word_length": 0,
	"pdf.max_bytes":                  1,
	"fetch.max_bytes":                1,
	"ocr.dpi":                        1,

	// Zero is meaningful here: no heap ceiling, no page ceiling, no minimum text per page.
	"app.memory_limit_mb":              0,
	"vocabulary.topics.max_names":      0,
	"vocabulary.source_tags.max_names": 0,
	"vocabulary.wiki_tags.max_names":   0,
	"pdf.max_pages":                    0,
	"ocr.max_pages":                    0,
	"ocr.min_text_chars_per_page":      0,
}

var scoreKeys = map[string]bool{
	"cluster.min_claim_score":    true,
	"cluster.merge_min_internal": true,
	"cluster.merge_min_gap":      true,
	"cluster.attach_min_score":   true,
	"vocabulary.shelf.max_share": true,
	"vocabulary.shelf.min_share": true,
}

// keyProblem checks ONE knob and returns a sentence naming it, or "". Both the writer (a single
// patch, before defaults can mask it) and Validate (the whole file) use it, so the two can never
// disagree about what a usable value is.
func keyProblem(key string, value any) string {
	switch key {
	case "language.ui", "language.claims", "language.summary":
		s, err := asString(value)
		if err != nil || !isLanguageCode(strings.ToLower(s)) {
			return fmt.Sprintf("%s: %q is not a language code", key, s)
		}
		return ""
	case "openai.model_compile", "openai.model_reason",
		"pdf.text_tool", "ocr.rasterizer", "ocr.tesseract":
		s, err := asString(value)
		if err != nil || s == "" {
			return key + ": must not be empty"
		}
		return ""
	case "openai.reasoning_effort_compile", "openai.reasoning_effort_distill", "openai.reasoning_effort_reason":
		s, err := asString(value)
		if err != nil || !slices.Contains(reasoningEfforts, s) {
			return fmt.Sprintf("%s: %q is not one of %s", key, s, strings.Join(reasoningEfforts, ", "))
		}
		return ""
	case "browser.wait_until":
		s, err := asString(value)
		if err != nil || !slices.Contains(waitUntilValues, s) {
			return fmt.Sprintf("%s: %q is not one of %s", key, s, strings.Join(waitUntilValues, ", "))
		}
		return ""
	case "ocr.languages":
		items, err := asStrings(value)
		if err != nil || len(items) == 0 {
			return key + ": must list at least one language"
		}
		return ""
	case "openai.blocked_windows":
		items, err := asStrings(value)
		if err != nil {
			return fmt.Sprintf("%s: %v", key, err)
		}
		if _, err := modelwindow.Parse(items); err != nil {
			return fmt.Sprintf("%s: %v", key, err)
		}
		return ""
	case "capture.change_min_percent":
		// A share, so 100 is the ceiling: a page cannot change more than entirely. Zero is
		// meaningful — it turns off only the share rule, leaving the absolute floor.
		n, err := asInt(value)
		if err != nil || n < 0 || n > 100 {
			return fmt.Sprintf("%s: %v is not a percentage", key, value)
		}
		return ""
	}

	if rule, ok := durationRules[key]; ok {
		s, err := asString(value)
		if err != nil {
			return fmt.Sprintf("%s: %v", key, err)
		}
		if s == "" {
			if rule.allowEmpty {
				return ""
			}
			return key + ": must be a duration"
		}
		d, dErr := time.ParseDuration(s)
		if dErr != nil {
			return fmt.Sprintf("%s: %v", key, dErr)
		}
		if d < 0 || (rule.positive && d == 0) {
			return fmt.Sprintf("%s: %s is not usable", key, s)
		}
		return ""
	}

	if minimum, ok := intRules[key]; ok {
		n, err := asInt(value)
		if err != nil || n < minimum {
			if minimum == 0 {
				return fmt.Sprintf("%s: must not be negative", key)
			}
			return fmt.Sprintf("%s: must be greater than zero", key)
		}
		return ""
	}

	if scoreKeys[key] {
		f, err := asFloat(value)
		if err != nil || f <= 0 || f > 1 {
			return fmt.Sprintf("%s: %v is outside (0,1]", key, value)
		}
	}

	return ""
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
