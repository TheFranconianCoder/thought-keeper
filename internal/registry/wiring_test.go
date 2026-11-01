package registry_test

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
	"github.com/TheFranconianCoder/thought-keeper/internal/stream"
)

// TestBuildWiresTheModelDependentUseCases: the domains that call the model carry their model name
// and their apply switch from the configuration, not from their zero value.
//
// This is the regression guard for a refactor that moved the use cases into domain structs: the
// values are built here, once, and a field that is forgotten reads as its zero value — a distill
// with no model refuses every document with "needs a model", which looks like a missing key.
func TestBuildWiresTheModelDependentUseCases(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Vault.Root = root
	cfg.OpenAI.APIKey = "sk-test"
	cfg.OpenAI.ModelCompile = "test-compile"
	cfg.OpenAI.ModelReason = "test-reason"

	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	deps := registry.Build(cfg, db, nil, nil, nil, nil, nil, nil, nil,
		func([]usecases.DiscoveredChildren) {}, nil, nil, nil, nil, nil, nil,
		stream.NewHub(), slog.New(slog.DiscardHandler))

	if got := deps.DocumentRun.Model; got != cfg.OpenAI.ModelCompile {
		t.Errorf("document run model = %q, want %q", got, cfg.OpenAI.ModelCompile)
	}
	if got := deps.Query.Model; got != cfg.OpenAI.ModelReason {
		t.Errorf("query model = %q, want %q", got, cfg.OpenAI.ModelReason)
	}
	if !deps.VocabularyCurate.Apply {
		t.Error("vocabulary curation would only measure: Apply is false")
	}
	if !deps.RebranchClaims.Apply {
		t.Error("rebranch would only measure: Apply is false")
	}
	if deps.RebranchClaims.MaxClaims <= 0 || deps.RebranchClaims.Parallel <= 0 {
		t.Errorf("rebranch bounds = (%d claims, %d parallel), want both > 0",
			deps.RebranchClaims.MaxClaims, deps.RebranchClaims.Parallel)
	}
}
