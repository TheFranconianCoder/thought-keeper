package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDefaults(t *testing.T) {
	if err := Validate(Defaults()); err != nil {
		t.Fatalf("the built-in defaults fail validation: %v", err)
	}
}

func TestWritePatchesAndKeepsCommentsAndReferences(t *testing.T) {
	clearOpenAIEnv(t)
	t.Setenv("TK_WRITE_KEY", "secret")
	path := writeConfig(t, `# the vault's language
language:
    ui: 'en'   # the interface language
openai:
    api_key: '${TK_WRITE_KEY}'
`)

	loaded, err := Write(path, Patch{"language.ui": "de"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if loaded.Config.Language.UI != "de" {
		t.Errorf("ui = %q, want de", loaded.Config.Language.UI)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"# the vault's language",
		"# the interface language",
		"${TK_WRITE_KEY}",
		"ui: de",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("written file lost %q:\n%s", want, text)
		}
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.Contains(string(backup), "ui: 'en'") {
		t.Errorf("backup does not hold the old bytes:\n%s", backup)
	}
}

func TestWriteCreatesAMissingFile(t *testing.T) {
	clearOpenAIEnv(t)
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")

	loaded, err := Write(path, Patch{"language.ui": "de"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !loaded.Exists {
		t.Error("the created file is reported as missing")
	}
	if loaded.Config.Language.UI != "de" {
		t.Errorf("ui = %q, want de", loaded.Config.Language.UI)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("a backup was written for a file that did not exist")
	}
}

func TestWriteCoercesJSONValues(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")

	loaded, err := Write(path, Patch{
		"pipeline.auto":          false,
		"pipeline.parallel_runs": float64(2),
		"ocr.languages":          []any{"eng", "deu"},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if loaded.Config.Pipeline.Auto {
		t.Error("pipeline.auto = true, want false")
	}
	if loaded.Config.Pipeline.ParallelRuns != 2 {
		t.Errorf("parallel_runs = %d, want 2", loaded.Config.Pipeline.ParallelRuns)
	}
	if got := strings.Join(loaded.Config.OCR.Languages, ","); got != "eng,deu" {
		t.Errorf("ocr.languages = %q, want eng,deu", got)
	}
}

func TestWriteRejectsWhatItMustNot(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		patch Patch
		want  error
	}{
		"unknown knob": {Patch{"vault.root": "/elsewhere"}, ErrNotWritable},
		"zero workers": {Patch{"pipeline.parallel_runs": 0}, ErrInvalidValue},
		"bad language": {Patch{"language.ui": "deutsch"}, ErrInvalidValue},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Write(path, tc.patch); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Error("a rejected patch changed the file")
			}
		})
	}
}

func TestWriteRefusesAnEnvironmentOwnedKnob(t *testing.T) {
	clearOpenAIEnv(t)
	t.Setenv("OPENAI_MODEL_COMPILE", "env-model")
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")

	if _, err := Write(path, Patch{"openai.model_compile": "file-model"}); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("err = %v, want ErrNotWritable", err)
	}
}

func TestValidateCollectsTheProblems(t *testing.T) {
	cfg := Defaults()
	cfg.Pipeline.ParallelRuns = 0
	cfg.Browser.WaitUntil = "whenever"
	cfg.Language.UI = "deutsch"
	cfg.Cluster.MinClaimScore = 2

	err := Validate(cfg)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("err = %v, want ErrInvalidValue", err)
	}
	for _, want := range []string{"parallel_runs", "wait_until", "language.ui", "min_claim_score"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error does not name %s: %v", want, err)
		}
	}
}
