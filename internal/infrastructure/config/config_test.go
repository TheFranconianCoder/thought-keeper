package config

import (
	"os"
	"path/filepath"
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

func TestOpenAIKeyFromEnvWhenConfigOmitsIt(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "from-env")
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")

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
