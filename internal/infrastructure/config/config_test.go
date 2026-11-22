package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:9210" {
		t.Errorf("addr = %q, want default", cfg.Server.Addr)
	}
	if got := cfg.SourcesDir(); filepath.Base(got) != "sources" {
		t.Errorf("SourcesDir = %q, want .../sources", got)
	}
	if got := cfg.WikiDir(); filepath.Base(got) != "wiki" {
		t.Errorf("WikiDir = %q, want .../wiki", got)
	}
	if !cfg.App.Tray || cfg.App.Autostart {
		t.Errorf("app defaults wrong: %+v", cfg.App)
	}
	if got := strings.Join(cfg.OpenAI.BlockedWindows, ","); got != "01:00-04:00,06:00-10:00" {
		t.Errorf("blocked_windows = %q, want the DeepSeek pair", got)
	}
}

// TestShelfQualityDefaults: the share limits have defaults, and a zero or negative value is not a
// limit the app can use, so it falls back to the default instead of marking every topic a catch-all.
func TestShelfQualityDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vocabulary.Shelf.MaxShare != 0.02 || cfg.Vocabulary.Shelf.MinShare != 0.0001 {
		t.Errorf("shelf shares = %v/%v, want 0.02/0.0001", cfg.Vocabulary.Shelf.MaxShare, cfg.Vocabulary.Shelf.MinShare)
	}
	if cfg.Vocabulary.Topics.MaxNames != 1000 {
		t.Errorf("topics target = %d, want 1000 (ADR 0010)", cfg.Vocabulary.Topics.MaxNames)
	}
	c := Defaults()
	c.Vocabulary.Shelf.MaxShare = 0
	c.Vocabulary.Shelf.MinShare = -1
	applyDefaults(&c)
	if c.Vocabulary.Shelf.MaxShare != 0.02 || c.Vocabulary.Shelf.MinShare != 0.0001 {
		t.Errorf("zero shares were not replaced by the defaults: %v/%v", c.Vocabulary.Shelf.MaxShare, c.Vocabulary.Shelf.MinShare)
	}
}

// TestAskBudgetDefaults: an answer is research, not a lookup, so Ask has its own turn budget (32)
// and the derivation keeps openai.max_iterations. A zero in the file is not a budget the app can
// use, so it falls back to the default.
func TestAskBudgetDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ask.MaxIterations != 32 {
		t.Errorf("ask.max_iterations = %d, want 32", cfg.Ask.MaxIterations)
	}
	if cfg.OpenAI.MaxIterations != 16 {
		t.Errorf("openai.max_iterations = %d, want the derivation's 16", cfg.OpenAI.MaxIterations)
	}
	c := Defaults()
	c.Ask.MaxIterations = 0
	applyDefaults(&c)
	if c.Ask.MaxIterations != 32 {
		t.Errorf("a zero ask budget was not replaced by the default: %d", c.Ask.MaxIterations)
	}
}

// TestBlockedWindowsCanBeEmptied: the default is a pair of windows, and a reader who wants the
// derivation around the clock says so with an explicit empty list — which must not fall back to the
// default again.
func TestBlockedWindowsCanBeEmptied(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "openai:\n    blocked_windows: []\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.BlockedWindows == nil || len(cfg.OpenAI.BlockedWindows) != 0 {
		t.Errorf("blocked_windows = %#v, want an explicit empty list", cfg.OpenAI.BlockedWindows)
	}
}

func TestLoadExpandsEnvAndHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TK_TEST_KEY", "secret")

	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "vault:\n    root: '~/vault'\nopenai:\n    api_key: '${TK_TEST_KEY}'\nserver:\n    addr: '127.0.0.1:0'\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.APIKey != "secret" {
		t.Errorf("api_key = %q, want secret", cfg.OpenAI.APIKey)
	}
	if want := filepath.Join(home, "vault"); cfg.VaultRoot() != want {
		t.Errorf("VaultRoot = %q, want %q", cfg.VaultRoot(), want)
	}
	if cfg.Server.Addr != "127.0.0.1:0" {
		t.Errorf("addr = %q, want override", cfg.Server.Addr)
	}
}

func TestPartialConfigKeepsDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n    addr: '127.0.0.1:9333'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:9333" {
		t.Errorf("addr = %q, want override", cfg.Server.Addr)
	}
	if cfg.PDF.TextTool != "pdftotext" {
		t.Errorf("pdf text tool = %q, want default", cfg.PDF.TextTool)
	}
}

func TestLanguageDefaultsAndOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n    addr: '127.0.0.1:9333'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Language.UI != "en" || cfg.Language.Claims != "en" || cfg.Language.Summary != "en" {
		t.Errorf("language defaults = %+v, want every value en", cfg.Language)
	}

	if err := os.WriteFile(p, []byte(`
language:
    ui: ' DE '
    claims: 'de'
    summary: 'fr'
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Language.UI != "de" || cfg.Language.Claims != "de" || cfg.Language.Summary != "fr" {
		t.Errorf("language = %+v, want de/de/fr (codes are trimmed and lower-cased)", cfg.Language)
	}

	if cfg.Pipeline.ParallelQueries <= 0 {
		t.Errorf("parallel queries = %d, want a positive default", cfg.Pipeline.ParallelQueries)
	}
}

func TestOpenAIKeyFromEnvWhenConfigOmitsIt(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "from-env")
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")
	// Each phase inherits the shared settings, so an explicit per-phase value must not leak in from
	// the machine running the test.
	t.Setenv("OPENAI_API_KEY_COMPILE", "")
	t.Setenv("OPENAI_API_KEY_REASON", "")

	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n    addr: '127.0.0.1:0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.APIKey != "from-env" {
		t.Errorf("api key = %q, want from-env", cfg.OpenAI.APIKey)
	}
	if cfg.OpenAI.BaseURL != "https://example.test/v1" {
		t.Errorf("base url = %q, want from-env", cfg.OpenAI.BaseURL)
	}
	// The regression this guards: the inheritance used to run BEFORE the environment was read, so it
	// copied an empty key and the document steps came up with "agent disabled" on a .env-only install.
	if cfg.OpenAI.APIKeyCompile != "from-env" || cfg.OpenAI.APIKeyReason != "from-env" {
		t.Errorf("phase keys = %q/%q, want the shared from-env",
			cfg.OpenAI.APIKeyCompile, cfg.OpenAI.APIKeyReason)
	}
	if cfg.OpenAI.BaseURLCompile != "https://example.test/v1" ||
		cfg.OpenAI.BaseURLReason != "https://example.test/v1" {
		t.Errorf("phase base urls = %q/%q, want the shared from-env",
			cfg.OpenAI.BaseURLCompile, cfg.OpenAI.BaseURLReason)
	}
}

// TestOpenAIPerPhaseOverridesTheSharedSettings: a phase may talk to its own endpoint with its own key
// (a cheap local model for the document steps, a strong one for Ask); what a phase leaves out falls
// back to the shared value.
func TestOpenAIPerPhaseOverridesTheSharedSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "shared-key")
	t.Setenv("OPENAI_BASE_URL", "https://shared.test/v1")
	t.Setenv("OPENAI_API_KEY_COMPILE", "compile-key")
	t.Setenv("OPENAI_BASE_URL_COMPILE", "https://compile.test/v1")
	t.Setenv("OPENAI_API_KEY_REASON", "")
	t.Setenv("OPENAI_BASE_URL_REASON", "")

	cfg, err := Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.APIKeyCompile != "compile-key" || cfg.OpenAI.BaseURLCompile != "https://compile.test/v1" {
		t.Errorf("compile phase = %q/%q, want its own key and endpoint",
			cfg.OpenAI.APIKeyCompile, cfg.OpenAI.BaseURLCompile)
	}
	if cfg.OpenAI.APIKeyReason != "shared-key" || cfg.OpenAI.BaseURLReason != "https://shared.test/v1" {
		t.Errorf("reason phase = %q/%q, want the shared key and endpoint",
			cfg.OpenAI.APIKeyReason, cfg.OpenAI.BaseURLReason)
	}
}

func TestModelsResolveFromEnv(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "shared-model")
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n    addr: '127.0.0.1:0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.ModelCompile != "shared-model" || cfg.OpenAI.ModelReason != "shared-model" {
		t.Errorf("models = %q/%q, want shared-model", cfg.OpenAI.ModelCompile, cfg.OpenAI.ModelReason)
	}
	t.Setenv("OPENAI_MODEL_COMPILE", "compile-only")
	cfg, _ = Load(p)
	if cfg.OpenAI.ModelCompile != "compile-only" || cfg.OpenAI.ModelReason != "shared-model" {
		t.Errorf("models = %q/%q, want compile-only/shared-model", cfg.OpenAI.ModelCompile, cfg.OpenAI.ModelReason)
	}
}

// TestPatienceKnobsFromEnv: a dev's .env (or the environment) must be able to set the knobs that
// decide how patient the caller is with a shared endpoint — and the distill reasoning, which is its
// own decision. They are one env var away, exactly like the model names.
func TestPatienceKnobsFromEnv(t *testing.T) {
	t.Setenv("OPENAI_TIMEOUT", "45s")
	t.Setenv("OPENAI_RETRIES", "5")
	t.Setenv("OPENAI_RETRY_BACKOFF", "500ms")
	t.Setenv("OPENAI_REASONING_EFFORT_DISTILL", "low")

	cfg, err := Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.Timeout != "45s" {
		t.Errorf("timeout = %q, want the env value", cfg.OpenAI.Timeout)
	}
	if cfg.OpenAI.Retries != 5 {
		t.Errorf("retries = %d, want 5", cfg.OpenAI.Retries)
	}
	if cfg.OpenAI.RetryBackoff != "500ms" {
		t.Errorf("retry_backoff = %q, want the env value", cfg.OpenAI.RetryBackoff)
	}
	if cfg.OpenAI.ReasoningEffortDistill != "low" {
		t.Errorf("distill effort = %q, want low", cfg.OpenAI.ReasoningEffortDistill)
	}
	// The compile phase keeps its own (mechanical) default.
	if cfg.OpenAI.ReasoningEffortCompile != "minimal" {
		t.Errorf("compile effort = %q, want minimal", cfg.OpenAI.ReasoningEffortCompile)
	}

	// An effort the file leaves EMPTY falls back to the compile effort — and the fallback is applied
	// after the environment was read, so it sees the env value instead of the pre-env empty one.
	t.Setenv("OPENAI_REASONING_EFFORT_DISTILL", "")
	t.Setenv("OPENAI_REASONING_EFFORT_COMPILE", "medium")
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("openai:\n    reasoning_effort_distill: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.ReasoningEffortCompile != "medium" {
		t.Errorf("compile effort = %q, want the env value", cfg.OpenAI.ReasoningEffortCompile)
	}
	if cfg.OpenAI.ReasoningEffortDistill != "medium" {
		t.Errorf("distill effort = %q, want the compile effort from the environment", cfg.OpenAI.ReasoningEffortDistill)
	}
}

// TestTheShippedExampleParses: the example config is what a user copies, so it is the one file that
// must always be loadable — and the loader is strict about unknown keys. A renamed knob that the
// example still carries fails here instead of silently doing nothing on someone's machine.
func TestTheShippedExampleParses(t *testing.T) {
	cfg, err := Load("../../../config.example.yaml")
	if err != nil {
		t.Fatalf("config.example.yaml does not load: %v", err)
	}
	// Spot-check that the pipeline part is the one being read (not silently defaulted).
	if cfg.Pipeline.PartsPerRun == 0 || !cfg.Pipeline.AutoDistill {
		t.Errorf("pipeline part was not read: %+v", cfg.Pipeline)
	}
	if cfg.Vault.Root == "" || cfg.Index.Path == "" {
		t.Errorf("vault/index not read: %+v %+v", cfg.Vault, cfg.Index)
	}
}

// TestUnknownConfigKeyFails: a typo must not be a silent default.
func TestUnknownConfigKeyFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("pipeline:\n    parts_per_runs: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("an unknown key must fail instead of being ignored")
	}
}

// TestTheOldIngestSectionIsGone: the rename has no alias, so an old file says so.
func TestTheOldIngestSectionIsGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("ingest:\n    auto: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("the removed `ingest:` part must be reported, not ignored")
	}
}
