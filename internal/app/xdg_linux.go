//go:build linux

package app

import (
	"os"
	"path/filepath"
	"strings"
)

// xdgDataHome is $XDG_DATA_HOME, or ~/.local/share when it is unset or relative (the XDG rule).
//
// It lives in its own Linux file because the smart-open looks for the launcher a browser wrote there
// when a PWA was installed.
func xdgDataHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); filepath.IsAbs(dir) {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "thought-keeper-data")
	}
	return filepath.Join(home, ".local", "share")
}
