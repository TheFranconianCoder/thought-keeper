package config

import (
	"errors"
	"strings"
	"testing"
)

// TestEveryWritableKeyIsDescribedAndWritable is the invariant behind two lists that would otherwise
// drift: a key in the allowlist must be a knob the overlay can describe (so the UI can offer it) and
// a value the writer accepts in its default shape.
func TestEveryWritableKeyIsDescribedAndWritable(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	view := Describe(loaded.Config, path)
	described := map[string]Field{}
	for _, section := range view.Sections {
		for _, field := range section.Fields {
			described[field.Key] = field
		}
	}
	values := valuesOf(Defaults())

	for _, key := range writableKeyList() {
		field, ok := described[key]
		if !ok {
			t.Errorf("%s is writable but the overlay does not describe it", key)
			continue
		}
		if field.Tier != Tier(key) || field.Tier == "" {
			t.Errorf("%s tier = %q, want %q", key, field.Tier, Tier(key))
		}
		value, ok := values[key]
		if !ok {
			t.Errorf("%s has no value in the defaults", key)
			continue
		}
		if _, err := Write(path, Patch{key: value}); err != nil {
			t.Errorf("writing the default of %s = %v: %v", key, value, err)
		}
	}
}

func TestKeyProblemAcceptsWhatTheAppCanUse(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"cluster.min_claim_score", 0.05},
		{"cluster.overlap_max", 2},
		{"browser.wait_until", "domcontentloaded"},
		{"pipeline.run_timeout", "0s"},
		{"pipeline.reconcile_interval", ""},
		{"assistant.prune_interval", ""},
		{"pdf.max_pages", 0},
		{"ocr.max_pages", 0},
		{"app.memory_limit_mb", 0},
		{"fetch.user_agent", ""},
		{"openai.reasoning_effort_distill", "low"},
	} {
		if problem := keyProblem(tc.key, tc.value); problem != "" {
			t.Errorf("%s = %v: %s", tc.key, tc.value, problem)
		}
	}
}

// TestKeyProblemRefusesValuesThatDefaultsWouldMask is the point of checking a patch BEFORE
// applyDefaults: a zero that the app silently replaces by a default is not written to the file.
func TestKeyProblemRefusesValuesThatDefaultsWouldMask(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"cluster.min_claim_score", 0},
		{"cluster.overlap_max", 0},
		{"capture.listing_max_pages", 0},
		{"capture.min_text_chars", 0},
		{"openai.retries", 0},
		{"openai.max_iterations", 0},
		{"assistant.max_items", 0},
		{"browser.timeout", "0s"},
		{"fetch.max_bytes", 0},
		{"ocr.dpi", 0},
		{"openai.timeout", ""},
		{"pdf.text_tool", ""},
		{"browser.wait_until", ""},
		{"browser.wait_until", "whenever"},
		{"pipeline.run_timeout", "ten minutes"},
		{"openai.reasoning_effort_reason", "maximal"},
	} {
		if problem := keyProblem(tc.key, tc.value); problem == "" {
			t.Errorf("%s = %v: accepted, want a problem", tc.key, tc.value)
		}
	}
}

func TestValidateNamesAdvancedProblems(t *testing.T) {
	cfg := Defaults()
	cfg.Cluster.NeighborK = 0
	cfg.Fetch.MaxBytes = 0
	cfg.Browser.WaitUntil = "whenever"

	err := Validate(cfg)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("err = %v, want ErrInvalidValue", err)
	}
	for _, want := range []string{"cluster.neighbor_k", "fetch.max_bytes", "browser.wait_until"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error does not name %s: %v", want, err)
		}
	}
}

func TestWriteAcceptsAnAdvancedPatch(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")

	loaded, err := Write(path, Patch{
		"cluster.neighbor_k":        float64(12),
		"browser.wait_until":        "domcontentloaded",
		"pipeline.run_timeout":      "20m",
		"capture.listing_max_pages": float64(5),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if loaded.Config.Cluster.NeighborK != 12 {
		t.Errorf("neighbor_k = %d, want 12", loaded.Config.Cluster.NeighborK)
	}
	if loaded.Config.Browser.WaitUntil != "domcontentloaded" {
		t.Errorf("wait_until = %q, want domcontentloaded", loaded.Config.Browser.WaitUntil)
	}
	if loaded.Config.Capture.ListingMaxPages != 5 {
		t.Errorf("listing_max_pages = %d, want 5", loaded.Config.Capture.ListingMaxPages)
	}
}
