// Package vaultgit handles the one thing the app does with a vault that is under version control:
// keeping the DERIVED files out of it.
//
// The vault is worth versioning (it is the knowledge: the sources, the atoms, the cluster
// membership), but the derived files are not: `.outline` is rebuildable, `.meta` and `.log` are
// records that change on every run, `index.db*` and `clusters/` are caches, and `.trash/` is a
// local bin. Committing them would bury the actual knowledge in churn.
//
// So when the vault is a git repository, the app makes sure the ignore rules are there. It only ever
// APPENDS what is missing: a user's own .gitignore lines are never touched, and a rule that is
// already there is left alone.
package vaultgit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Header marks the block this app is responsible for, so a reader of the file knows where the rules
// come from (and that they may be edited, but will come back if removed).
const Header = "# thought-keeper: derived files — rebuildable or rewritten on every run (see AGENTS.md → Sidecars)"

// Patterns are the paths that are derived rather than knowledge. Everything else in the vault (the
// objects in sources/, the atom files, the vocabulary) IS the artifact and belongs in the history.
func Patterns() []string {
	return []string{
		// The vault's hidden cache (favicons) and the local trash.
		".thought-keeper/",
		".trash/",
		// The drop zone: files are consumed by intake, so a leftover there is not content.
		"inbox/",
		// The index: a performance layer, rebuildable with `reindex` + `rebuild-trees`.
		"index.db",
		"index.db-wal",
		"index.db-shm",
		// A source's sidecars, next to the object — the readable rendering included: it is derived
		// (re-extracting the same bytes produces the same text), so it is not knowledge and does not
		// belong in the history. Its name says which step wrote it (<hash>.capture.md), which is what
		// makes it excludable at all — a plain <hash>.md would have taken the Markdown SOURCES with it.
		// The earlier names are ignored too, for vaults that still carry them.
		"sources/**/*.capture.md",
		"sources/**/*.text.md",
		"sources/**/*.meta",
		"sources/**/*.outline",
		"sources/**/*.log",
		"sources/**/*.log.1",
		// The association layer is a cache: the claims are the truth.
		"clusters/**/*.meta",
	}
}

// IsRepo reports whether the directory is inside a git repository (a .git entry exists).
func IsRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// EnsureIgnore makes sure root's .gitignore excludes every pattern, and reports how many lines it
// had to add (0 = the file was already complete). Missing file: it is created. Existing lines are
// preserved verbatim, so a vault's own rules survive.
func EnsureIgnore(root string, patterns []string) (int, error) {
	path := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("vaultgit: read .gitignore: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	have := make(map[string]bool, len(lines))
	hasHeader := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		have[t] = true
		if t == Header {
			hasHeader = true
		}
	}

	var missing []string
	for _, p := range patterns {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return 0, nil
	}

	out := strings.TrimRight(string(content), "\n")
	var b strings.Builder
	if out != "" {
		b.WriteString(out)
		b.WriteString("\n")
	}
	if !hasHeader {
		b.WriteString(Header + "\n")
	}
	for _, p := range missing {
		b.WriteString(p + "\n")
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return 0, fmt.Errorf("vaultgit: write .gitignore: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, fmt.Errorf("vaultgit: rename .gitignore: %w", err)
	}
	return len(missing), nil
}
