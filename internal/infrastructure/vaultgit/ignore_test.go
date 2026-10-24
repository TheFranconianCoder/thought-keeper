package vaultgit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureIgnoreCreatesTheRules: a vault under version control must not collect derived-file
// churn — the app puts the rules in place when the repository exists.
func TestEnsureIgnoreCreatesTheRules(t *testing.T) {
	root := t.TempDir()
	if IsRepo(root) {
		t.Fatal("a fresh directory must not look like a repository")
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsRepo(root) {
		t.Fatal("a directory with .git must be a repository")
	}

	n, err := EnsureIgnore(root, Patterns())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if n != len(Patterns()) {
		t.Errorf("added %d rules, want %d", n, len(Patterns()))
	}
	raw, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("no .gitignore was written: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, Header) {
		t.Errorf("the block is not marked:\n%s", content)
	}
	for _, p := range Patterns() {
		if !strings.Contains(content, p+"\n") {
			t.Errorf("rule %q is missing:\n%s", p, content)
		}
	}

	// Idempotent: a second run adds nothing.
	again, err := EnsureIgnore(root, Patterns())
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Errorf("second run added %d rules, want 0", again)
	}
}

// TestEnsureIgnorePreservesTheUsersRules: the app only APPENDS what is missing. A vault's own
// .gitignore is the user's file.
func TestEnsureIgnorePreservesTheUsersRules(t *testing.T) {
	root := t.TempDir()
	own := "*.bak\n# my own note\nindex.db\n" // one rule is already there, in the user's own words
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := EnsureIgnore(root, Patterns())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || n == len(Patterns()) {
		t.Errorf("added %d rules, want the missing ones only", n)
	}
	raw, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	content := string(raw)
	if !strings.HasPrefix(content, own) {
		t.Errorf("the user's lines were changed:\n%s", content)
	}
	if strings.Count(content, "\nindex.db\n") != 1 {
		t.Errorf("an existing rule was added twice:\n%s", content)
	}
	if !strings.Contains(content, "sources/**/*.log\n") {
		t.Errorf("a missing rule was not added:\n%s", content)
	}
}

// TestPatternsCoverTheSidecars: the rule list and the code that writes the sidecars must not drift —
// every derived file of a source is in the list.
func TestPatternsCoverTheSidecars(t *testing.T) {
	joined := strings.Join(Patterns(), "\n")
	for _, suffix := range []string{".capture.md", ".text.md", ".meta", ".outline", ".log"} {
		if !strings.Contains(joined, suffix) {
			t.Errorf("the sidecar suffix %s is not ignored", suffix)
		}
	}
	for _, derived := range []string{"index.db", ".thought-keeper/", ".trash/", "inbox/", "clusters/"} {
		if !strings.Contains(joined, derived) {
			t.Errorf("%s is not ignored", derived)
		}
	}
}
