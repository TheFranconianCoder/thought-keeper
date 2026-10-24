package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvDoesNotOverrideRealEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("TK_DOTENV_A=one\nTK_DOTENV_B=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TK_DOTENV_B", "keep")

	loaded := LoadDotEnv(filepath.Join(dir, "missing.env"), p)
	if len(loaded) != 1 || loaded[0] != p {
		t.Fatalf("loaded = %v, want [%s]", loaded, p)
	}
	if got := os.Getenv("TK_DOTENV_A"); got != "one" {
		t.Errorf("TK_DOTENV_A = %q, want one", got)
	}
	if got := os.Getenv("TK_DOTENV_B"); got != "keep" {
		t.Errorf("TK_DOTENV_B = %q, want keep (real env must win)", got)
	}
}
