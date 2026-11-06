package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearOpenAIEnv keeps a developer's shell out of the assertions: every knob this file inspects is
// either set by the test or absent.
func clearOpenAIEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL", "OPENAI_MODEL_COMPILE",
		"OPENAI_MODEL_REASON", "OPENAI_REASONING_EFFORT_COMPILE", "OPENAI_REASONING_EFFORT_DISTILL",
		"OPENAI_REASONING_EFFORT_REASON", "OPENAI_TIMEOUT", "OPENAI_RETRIES", "OPENAI_RETRY_BACKOFF",
	} {
		t.Setenv(name, "")
	}
}

// writeConfig writes a machine config with a vault of its own. Every config in this package needs a
// private vault root: a vault-scoped key is written to <vault>/vault.yaml, and one test's vault must
// not leak into the next.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf("vault:\n    root: %q\n", filepath.Join(dir, "vault")) + body
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func findField(t *testing.T, view View, key string) Field {
	t.Helper()
	for _, section := range view.Sections {
		for _, field := range section.Fields {
			if field.Key == key {
				return field
			}
		}
	}
	t.Fatalf("field %q is not in the view", key)
	return Field{}
}

func TestLoadFullReportsTheFilesKeys(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, `
language:
    claims: 'de'
openai:
    model_compile: 'file-model'
`)

	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	if !loaded.Exists || loaded.Path != path {
		t.Errorf("exists/path = %v/%q, want true/%q", loaded.Exists, loaded.Path, path)
	}
	for _, key := range []string{"language.claims", "openai.model_compile"} {
		if !loaded.Set[key] {
			t.Errorf("%q is not marked as declared by the file", key)
		}
	}
	for _, key := range []string{"language.summary", "capture.refresh_interval"} {
		if loaded.Set[key] {
			t.Errorf("%q is marked as declared, but the file does not set it", key)
		}
	}
	if len(loaded.Env) != 0 {
		t.Errorf("env provenance = %v, want none", loaded.Env)
	}
	if loaded.Config.Language.Claims != "de" {
		t.Errorf("claims language = %q, want de", loaded.Config.Language.Claims)
	}
}

func TestLoadFullMissingFileIsDefaults(t *testing.T) {
	clearOpenAIEnv(t)
	path := filepath.Join(t.TempDir(), "missing.yaml")

	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	if loaded.Exists {
		t.Error("a missing file is reported as existing")
	}
	if loaded.Path != path {
		t.Errorf("path = %q, want %q", loaded.Path, path)
	}
	if loaded.Config.Server.Addr != Defaults().Server.Addr {
		t.Errorf("addr = %q, want the default", loaded.Config.Server.Addr)
	}
	if len(loaded.Set) != 0 {
		t.Errorf("declared keys = %v, want none", loaded.Set)
	}
}

func TestLoadFullMarksTheEnvironment(t *testing.T) {
	clearOpenAIEnv(t)
	t.Setenv("OPENAI_MODEL_COMPILE", "env-model")
	path := writeConfig(t, "openai:\n    model_compile: 'file-model'\n")

	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	if loaded.Config.OpenAI.ModelCompile != "env-model" {
		t.Errorf("model = %q, want the environment's env-model", loaded.Config.OpenAI.ModelCompile)
	}
	if got := loaded.Env["openai.model_compile"]; got != "OPENAI_MODEL_COMPILE" {
		t.Errorf("env provenance = %q, want OPENAI_MODEL_COMPILE", got)
	}
}

func TestDescribeShowsRunningValuesAndDrift(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "language:\n    claims: 'de'\n")

	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	same := Describe(loaded.Config, path)
	if same.Changed {
		t.Error("an unchanged file is reported as drift")
	}
	if got := findField(t, same, "language.claims"); got.Value != "de" || got.Source != "file" {
		t.Errorf("claims = %#v (source %q), want de from the file", got.Value, got.Source)
	}
	if got := findField(t, same, "language.summary"); got.Source != "default" {
		t.Errorf("summary source = %q, want default", got.Source)
	}

	// An edit over SSH: the process keeps running the old values, the view says so.
	if err := os.WriteFile(path, []byte("language:\n    claims: 'fr'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drift := Describe(loaded.Config, path)
	if !drift.Changed {
		t.Error("an edited file is not reported as drift")
	}
	if got := findField(t, drift, "language.claims"); got.Value != "de" {
		t.Errorf("claims = %v, want the RUNNING value de", got.Value)
	}
}

func TestDescribeRedactsSecrets(t *testing.T) {
	clearOpenAIEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-do-not-print")
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")

	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	view := Describe(loaded.Config, path)
	key := findField(t, view, "openai.api_key")
	if !key.Secret || !key.Set || key.Value != nil {
		t.Errorf("api_key field = %#v, want a set secret with no value", key)
	}
	if key.Source != "environment" || key.Env != "OPENAI_API_KEY" {
		t.Errorf("api_key origin = %q/%q, want environment/OPENAI_API_KEY", key.Source, key.Env)
	}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	if strings.Contains(string(body), "sk-do-not-print") {
		t.Error("the secret is in the wire payload")
	}
}

func TestDescribeHeadersOnlyNameThemselves(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "openai:\n    headers:\n        Authorization: 'Bearer sk-secret'\n")

	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	view := Describe(loaded.Config, path)
	headers := findField(t, view, "openai.headers")
	names, ok := headers.Value.([]string)
	if !ok || len(names) != 1 || names[0] != "Authorization" {
		t.Errorf("header value = %#v, want the names only", headers.Value)
	}
	body, _ := json.Marshal(view) //nolint:errcheck // the view is marshaled for a substring check
	if strings.Contains(string(body), "sk-secret") {
		t.Error("a header value is in the wire payload")
	}
}

func TestDescribeReportsAnUnreadableFile(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "language:\n    claims: 'de'\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	if err := os.WriteFile(path, []byte("language: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view := Describe(loaded.Config, path)
	if view.Error == "" {
		t.Error("a broken file is not reported")
	}
	if !view.Exists {
		t.Error("a file that exists is reported as missing because it is broken")
	}
	if view.Changed {
		t.Error("a broken file must not claim drift: it cannot be compared")
	}
	if got := findField(t, view, "language.claims"); got.Value != "de" {
		t.Errorf("claims = %v, want the running value", got.Value)
	}
}

func TestDescribeNamesKindsAndApplyBehaviour(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "language:\n    claims: 'de'\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	view := Describe(loaded.Config, path)

	cases := []struct {
		key     string
		kind    string
		restart bool
	}{
		{"capture.refresh_interval", "duration", false},
		{"pipeline.auto_distill", "bool", false},
		{"pipeline.auto", "bool", false},
		{"vault.root", "path", true},
		{"vault.trash", "path", true},
		{"vault.git.ignore_derived", "bool", true},
		{"ocr.languages", "list", false},
		{"pdf.text_tool", "tool", false},
		{"ocr.rasterizer", "tool", false},
		{"browser.path", "tool", false},
		{"browser.wait_until", "string", false},
		{"cluster.neighbor_k", "int", false},
		{"server.addr", "string", true},
	}
	for _, tc := range cases {
		field := findField(t, view, tc.key)
		if field.Kind != tc.kind {
			t.Errorf("%s kind = %q, want %q", tc.key, field.Kind, tc.kind)
		}
		if field.Restart != tc.restart {
			t.Errorf("%s restart = %v, want %v", tc.key, field.Restart, tc.restart)
		}
		if field.Writable != Writable(tc.key) {
			t.Errorf("%s writable = %v, want %v", tc.key, field.Writable, Writable(tc.key))
		}
		if field.Tier != Tier(tc.key) {
			t.Errorf("%s tier = %q, want %q", tc.key, field.Tier, Tier(tc.key))
		}
	}
	if got := findField(t, view, "browser.wait_until").Options; len(got) != 5 {
		t.Errorf("wait_until options = %v, want five", got)
	}
	if got := findField(t, view, "language.claims").Tier; got != TierBasic {
		t.Errorf("language.claims tier = %q, want %q", got, TierBasic)
	}
	if got := findField(t, view, "cluster.neighbor_k").Tier; got != TierAdvanced {
		t.Errorf("cluster.neighbor_k tier = %q, want %q", got, TierAdvanced)
	}
	if got := findField(t, view, "vault.root").Tier; got != "" {
		t.Errorf("vault.root tier = %q, want empty (read-only)", got)
	}
	if len(view.Sections) == 0 || view.Sections[0].Name != "app" {
		t.Errorf("first section = %+v, want the struct's order (app)", view.Sections)
	}
}

// TestDescribeResolvesAbsolutePathsAndTools: a path knob shows the directory the process uses, and a
// tool knob shows the binary it runs — or that it did not find one. A bare "pdftotext" is not an
// answer to "which binary?".
func TestDescribeResolvesAbsolutePathsAndTools(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	view := Describe(loaded.Config, path)

	for _, key := range []string{
		"vault.root", "vault.sources", "vault.inbox", "vault.atoms", "vault.wiki",
		"vault.clusters", "vault.asks", "vault.commands", "vault.vocabulary",
		"vault.interests", "vault.trash", "index.path", "index.favicons",
	} {
		field := findField(t, view, key)
		if field.Resolved == "" || !filepath.IsAbs(field.Resolved) {
			t.Errorf("%s resolved = %q, want an absolute path", key, field.Resolved)
		}
	}
	if got := filepath.Base(findField(t, view, "vault.sources").Resolved); got != "sources" {
		t.Errorf("vault.sources resolves to %q, want .../sources", got)
	}

	lookups := map[string]string{
		"pdf.text_tool": "pdftotext", "ocr.rasterizer": "pdftocairo",
		"ocr.tesseract": "tesseract", "browser.path": "lightpanda",
	}
	for key, wantLookup := range lookups {
		field := findField(t, view, key)
		if field.Kind != "tool" {
			t.Errorf("%s kind = %q, want tool", key, field.Kind)
		}
		if field.Lookup != wantLookup {
			t.Errorf("%s lookup = %q, want %q", key, field.Lookup, wantLookup)
		}
		// The tools are not guaranteed on the machine running the tests: an empty result means the
		// overlay says "not found". A result must be an absolute path.
		if field.Resolved != "" && !filepath.IsAbs(field.Resolved) {
			t.Errorf("%s resolved = %q, want an absolute path or empty", key, field.Resolved)
		}
	}
}

func TestResolvedToolPath(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	cfg := Defaults()
	cfg.PDF.TextTool = exe
	if got := resolvedPath("pdf.text_tool", cfg); got == "" || !filepath.IsAbs(got) {
		t.Errorf("an absolute configured tool resolved to %q, want an absolute path", got)
	}
	if got := toolLookup("pdf.text_tool", cfg); got != exe {
		t.Errorf("lookup = %q, want the configured path", got)
	}

	cfg.PDF.TextTool = "thought-keeper-no-such-tool"
	if got := resolvedPath("pdf.text_tool", cfg); got != "" {
		t.Errorf("a missing tool resolved to %q, want empty", got)
	}

	// An empty browser path is the engine's own name, looked up on PATH — never "nowhere".
	cfg.Browser.Path = ""
	if got := toolLookup("browser.path", cfg); got != "lightpanda" {
		t.Errorf("an empty browser path looks up %q, want lightpanda", got)
	}
	got := resolvedPath("browser.path", cfg)
	if got != "" && !filepath.IsAbs(got) {
		t.Errorf("the default browser resolved to %q, want an absolute path or empty", got)
	}
}
