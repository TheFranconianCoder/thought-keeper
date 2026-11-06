package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeVault writes the vault file of a config loaded from path. The directory exists in the tests
// because writeConfig gives every config a private vault root.
func writeVault(t *testing.T, vaultPath, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(vaultPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func TestVaultWinsAndFallsBack(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "language:\n    claims: 'en'\ncluster:\n    neighbor_k: 16\n")
	before, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	writeVault(t, before.VaultPath, "language:\n    claims: 'de'\n")

	merged, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	if merged.Config.Language.Claims != "de" {
		t.Errorf("claims = %q, want the vault's de", merged.Config.Language.Claims)
	}
	if merged.Config.Cluster.NeighborK != 16 {
		t.Errorf("neighbor_k = %d, want the machine's 16 (the vault leaves it out)", merged.Config.Cluster.NeighborK)
	}
	if !merged.VaultExists || merged.VaultPath != before.VaultPath {
		t.Errorf("vault = %v/%q, want true/%q", merged.VaultExists, merged.VaultPath, before.VaultPath)
	}
	if !merged.VaultSet["language.claims"] || merged.VaultSet["cluster.neighbor_k"] {
		t.Errorf("vault set = %v, want only language.claims", merged.VaultSet)
	}
}

func TestVaultRefusesAMachineKey(t *testing.T) {
	clearOpenAIEnv(t)
	for name, body := range map[string]string{
		"language.ui":       "language:\n    ui: 'de'\n",
		"vault.root":        "vault:\n    root: '/tmp/elsewhere'\n",
		"pipeline.parallel": "pipeline:\n    parallel_runs: 8\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "language:\n    claims: 'en'\n")
			loaded, err := LoadFull(path)
			if err != nil {
				t.Fatalf("LoadFull: %v", err)
			}
			writeVault(t, loaded.VaultPath, body)
			if _, err := LoadFull(path); !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("err = %v, want ErrInvalidValue", err)
			} else if !strings.Contains(err.Error(), name) {
				t.Errorf("error does not name %s: %v", name, err)
			}
		})
	}
}

func TestDescribeReportsTheVaultOriginAndScope(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "language:\n    claims: 'en'\n")
	running, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	writeVault(t, running.VaultPath, "language:\n    claims: 'de'\n")

	view := Describe(running.Config, path)
	claims := findField(t, view, "language.claims")
	if claims.Value != "en" {
		t.Errorf("claims value = %v, want the RUNNING en", claims.Value)
	}
	if claims.Source != "vault" {
		t.Errorf("claims source = %q, want vault (the file on disk declares it)", claims.Source)
	}
	if claims.Scope != "vault" {
		t.Errorf("claims scope = %q, want vault", claims.Scope)
	}
	if ui := findField(t, view, "language.ui"); ui.Scope != "machine" {
		t.Errorf("language.ui scope = %q, want machine", ui.Scope)
	}
	if !view.VaultExists || view.VaultPath != running.VaultPath {
		t.Errorf("view vault = %v/%q, want true/%q", view.VaultExists, view.VaultPath, running.VaultPath)
	}
	if !view.Changed {
		t.Error("the vault file differs from the running config, but no drift is reported")
	}
}

func TestWriteSplitsThePatchAcrossTheTwoFiles(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "language:\n    claims: 'en'\ncluster:\n    neighbor_k: 16\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}

	res, err := Write(path, Patch{
		"language.claims":        "de",
		"cluster.neighbor_k":     float64(12),
		"pipeline.parallel_runs": float64(2),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !res.VaultExists {
		t.Error("the vault file was not created")
	}
	if res.Config.Language.Claims != "de" || res.Config.Cluster.NeighborK != 12 {
		t.Errorf("claims/neighbor = %q/%d, want de/12", res.Config.Language.Claims, res.Config.Cluster.NeighborK)
	}

	vaultBody := readFile(t, loaded.VaultPath)
	for _, want := range []string{"# Settings that belong to this vault", "claims: de", "neighbor_k: 12"} {
		if !strings.Contains(vaultBody, want) {
			t.Errorf("vault file lost %q:\n%s", want, vaultBody)
		}
	}
	machineBody := readFile(t, path)
	if !strings.Contains(machineBody, "parallel_runs: 2") {
		t.Errorf("machine file lost pipeline.parallel_runs:\n%s", machineBody)
	}
	if strings.Contains(machineBody, "claims: de") || strings.Contains(machineBody, "neighbor_k: 12") {
		t.Errorf("a vault key was written to the machine file:\n%s", machineBody)
	}

	// The vault file keeps its own backup, like the machine's config.
	if _, err := Write(path, Patch{"language.claims": "fr"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if backup := readFile(t, loaded.VaultPath+".bak"); !strings.Contains(backup, "claims: de") {
		t.Errorf("the vault backup does not hold the previous bytes:\n%s", backup)
	}
}

func TestWriteCreatesTheVaultWithOnlyThePatchedKeys(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	if loaded.VaultExists {
		t.Fatal("the test vault already exists")
	}

	if _, err := Write(path, Patch{"language.claims": "de"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	vaultBody := readFile(t, loaded.VaultPath)
	if !strings.Contains(vaultBody, "claims: de") {
		t.Errorf("vault file does not hold the edited key:\n%s", vaultBody)
	}
	if strings.Contains(vaultBody, "cluster:") || strings.Contains(vaultBody, "capture:") {
		t.Errorf("the vault was seeded with keys the patch did not touch:\n%s", vaultBody)
	}

	// A second save grows the same file and keeps its header.
	if _, err := Write(path, Patch{"cluster.neighbor_k": float64(12)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	vaultBody = readFile(t, loaded.VaultPath)
	if !strings.Contains(vaultBody, "claims: de") || !strings.Contains(vaultBody, "neighbor_k: 12") {
		t.Errorf("the second save lost a key:\n%s", vaultBody)
	}
	if strings.Count(vaultBody, "# Settings that belong") != 1 {
		t.Errorf("the header was duplicated:\n%s", vaultBody)
	}
}

func TestARejectedMixedPatchWritesNeitherFile(t *testing.T) {
	clearOpenAIEnv(t)
	path := writeConfig(t, "server:\n    addr: '127.0.0.1:9210'\n")
	loaded, err := LoadFull(path)
	if err != nil {
		t.Fatalf("LoadFull: %v", err)
	}
	before := readFile(t, path)

	_, err = Write(path, Patch{
		"language.claims":        "de",
		"pipeline.parallel_runs": float64(0),
	})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("err = %v, want ErrInvalidValue", err)
	}
	if got := readFile(t, path); got != before {
		t.Error("the rejected patch changed the machine file")
	}
	if _, statErr := os.Stat(loaded.VaultPath); !os.IsNotExist(statErr) {
		t.Error("the rejected patch created the vault file")
	}
}
