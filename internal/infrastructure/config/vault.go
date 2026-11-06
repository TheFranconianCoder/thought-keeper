package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// vaultKeys are the knobs that belong to the VAULT, not to the machine: the language the agent writes
// in, how often this vault's URLs are re-read, what counts as a readable page, the OCR languages, the
// assistant's history window, and the cluster arithmetic. They describe the corpus and the association
// layer, so they travel with the vault in <vault>/vault.yaml.
//
// The machine's config remains the fallback for a vault that has no vault.yaml (an existing install
// keeps working), and for a key the vault file does not declare. A key the vault file DOES declare
// wins over the machine.
var vaultKeys = map[string]bool{
	"language.claims":               true,
	"language.summary":              true,
	"capture.refresh_interval":      true,
	"capture.feed_refresh_interval": true,
	"capture.min_text_chars":        true,
	"capture.listing_max_pages":     true,
	"cluster.neighbor_k":            true,
	"cluster.min_claim_score":       true,
	"cluster.merge_min_internal":    true,
	"cluster.merge_min_gap":         true,
	"cluster.split_min_core_size":   true,
	"cluster.topic_span_limit":      true,
	"cluster.max_cluster_claims":    true,
	"cluster.attach_min_score":      true,
	"cluster.overlap_min_edges":     true,
	"cluster.overlap_max":           true,
	"cluster.cross_topic_min_edges": true,
	"ocr.languages":                 true,
	"assistant.retention":           true,
}

// VaultScoped reports whether a knob belongs in the vault's own config file.
func VaultScoped(key string) bool { return vaultKeys[key] }

// vaultPathName is the vault's own config file, next to index.db.
const vaultPathName = "vault.yaml"

// VaultPath is the vault's config file for a configuration: <vault root>/vault.yaml.
func VaultPath(c Config) string { return filepath.Join(c.VaultRoot(), vaultPathName) }

// machineConfig decodes the machine's YAML over the code's defaults. An absent file is the defaults;
// unknown keys are refused, because a typo must not leave a setting at its default while the file
// says otherwise.
func machineConfig(data []byte) (Config, error) {
	cfg := Defaults()
	if len(data) == 0 {
		return cfg, nil
	}
	dec := yaml.NewDecoder(strings.NewReader(os.ExpandEnv(string(data))))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return cfg, nil
		}
		return cfg, err
	}
	return cfg, nil
}

// mergeVault overlays the vault file on cfg. Only vault-scoped keys are accepted: a machine key in
// vault.yaml is an error, not a silent ignore — the reader put it in the vault's file on purpose, and
// the app must say that the file cannot carry it.
//
// It returns the dotted keys the file declared, so the overlay can say where a value came from.
func mergeVault(cfg *Config, data []byte) (map[string]bool, error) {
	text := os.ExpandEnv(string(data))
	set := map[string]bool{}
	for key := range declaredKeys(text) {
		if !strings.Contains(key, ".") {
			continue // a section mapping (language, cluster, ...)
		}
		if !VaultScoped(key) {
			return nil, fmt.Errorf("%w: %s does not belong in %s", ErrInvalidValue, key, vaultPathName)
		}
		set[key] = true
	}
	dec := yaml.NewDecoder(strings.NewReader(text))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", vaultPathName, err)
	}
	return set, nil
}

// mergedConfig is the effective configuration for a pair of file contents, without touching disk: the
// machine file over the defaults, the vault file over that. Callers that need the final values call
// applyDefaults; callers that only validate the result call it before Validate.
func mergedConfig(machineData, vaultData []byte) (Config, error) {
	cfg, err := machineConfig(machineData)
	if err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	if len(vaultData) > 0 {
		if _, err := mergeVault(&cfg, vaultData); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}
