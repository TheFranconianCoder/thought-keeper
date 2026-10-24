//go:build linux

package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On Linux a Chromium-family browser (Chrome, Chromium, Edge) that installs a PWA writes a .desktop
// file into the user's application directory. It is a real launcher — the desktop shows it in the
// app list — and starting it opens the PWA in its own window. Finding that file is all "smart open"
// is: there is no browser API to ask for "the installed PWA", and the file the browser already wrote
// is exactly it.

// findPWALauncher scans the XDG application directories for the launcher of the PWA named name.
func findPWALauncher(name string) (pwaLauncher, bool) {
	return findPWALauncherDirs(pwaLauncherDirs(), name)
}

// findPWALauncherDirs is findPWALauncher over an explicit directory list, so it can be tested
// without reading the user's real application data.
func findPWALauncherDirs(dirs []string, name string) (pwaLauncher, bool) {
	want := strings.TrimSpace(name)
	if want == "" {
		return pwaLauncher{}, false
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			de, ok := parseDesktopEntry(string(data))
			if !ok || de.noDisplay || de.hidden {
				continue
			}
			// A Chromium PWA is launched with `--app-id=<id>`. The browser's own entry has none, so
			// the browser itself can never be mistaken for the app.
			if !isChromiumPWALauncher(de.exec) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(de.name), want) {
				continue
			}
			return pwaLauncher{Path: path}, true
		}
	}
	return pwaLauncher{}, false
}

// pwaLauncherDirs are the directories a browser writes an installed PWA's .desktop file into, in the
// order XDG puts them: the user's own data home first (where an install lands), then the system data
// dirs (a PWA installed system-wide, unusual but possible).
func pwaLauncherDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	add(filepath.Join(xdgDataHome(), "applications"))
	raw := os.Getenv("XDG_DATA_DIRS")
	if raw == "" {
		// The XDG default when the variable is unset.
		raw = "/usr/local/share:/usr/share"
	}
	for _, d := range filepath.SplitList(raw) {
		if strings.TrimSpace(d) != "" {
			add(filepath.Join(d, "applications"))
		}
	}
	return dirs
}

// isChromiumPWALauncher reports whether an Exec line starts a Chromium-family browser in app mode,
// which is how the browser launches an INSTALLED PWA.
func isChromiumPWALauncher(exec string) bool {
	return strings.Contains(exec, "--app-id=")
}

// launchPWALauncher starts a .desktop launcher the way the desktop itself would. `gio launch` is the
// GLib tool that reads the entry and starts it with the spec's quoting; `gtk-launch` is the older
// equivalent. When neither is installed the launch fails and the caller falls back to the URL
// handler — nothing is lost, because that is what happened before smart open.
func launchPWALauncher(l pwaLauncher) error {
	if _, err := exec.LookPath("gio"); err == nil {
		return exec.Command("gio", "launch", l.Path).Start()
	}
	if _, err := exec.LookPath("gtk-launch"); err == nil {
		id := strings.TrimSuffix(filepath.Base(l.Path), ".desktop")
		return exec.Command("gtk-launch", id).Start()
	}
	return errors.New("neither gio nor gtk-launch is available")
}

// desktopEntry is the little of a .desktop file the discovery needs.
type desktopEntry struct {
	name      string
	exec      string
	noDisplay bool
	hidden    bool
}

// parseDesktopEntry reads the [Desktop Entry] group of a .desktop file. A minimal INI reader is
// enough: the keys are simple, and a file that is not well-formed simply yields nothing — never an
// error the user would have to deal with, because the caller falls back to the OS handler.
//
// The unlocalized `Name` is the one that is read; `Name[de]=…` and friends are ignored, so the entry
// is matched by the name every locale agrees on.
func parseDesktopEntry(data string) (desktopEntry, bool) {
	var (
		out  desktopEntry
		name bool
	)
	inGroup := false
	for raw := range strings.SplitSeq(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inGroup = line == "[Desktop Entry]"
			continue
		}
		if !inGroup {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			out.name, name = strings.TrimSpace(value), true
		case "Exec":
			out.exec = strings.TrimSpace(value)
		case "NoDisplay":
			out.noDisplay = strings.EqualFold(strings.TrimSpace(value), "true")
		case "Hidden":
			out.hidden = strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return out, name
}
