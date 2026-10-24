//go:build linux

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDesktop(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The launcher Chrome writes for an installed PWA: a .desktop whose Exec starts the browser with
// --app-id, and whose Name is the manifest's name. This is the file smart open looks for.
const chromePWALauncher = "[Desktop Entry]\n" +
	"Version=1.0\n" +
	"Type=Application\n" +
	"Name=Thought Keeper\n" +
	"Exec=/usr/bin/google-chrome --profile-directory=Default --app-id=abcdefghijklmnopabcdefghijklmnop\n" +
	"StartupWMClass=crx__abcdefghijklmnopabcdefghijklmnop\n" +
	"Icon=chrome-abcdefghijklmnopabcdefghijklmnop-Default\n"

func TestFindPWALauncherDirsFindsChromePWA(t *testing.T) {
	dir := t.TempDir()
	writeDesktop(t, dir, "chrome-abcdefghijklmnopabcdefghijklmnop-Default.desktop", chromePWALauncher)
	// The browser's own entry: same Name, but no --app-id — the browser itself must never match.
	writeDesktop(t, dir, "google-chrome.desktop",
		"[Desktop Entry]\nName=Thought Keeper\nExec=/usr/bin/google-chrome %U\n")

	got, ok := findPWALauncherDirs([]string{dir}, "Thought Keeper")
	if !ok {
		t.Fatal("expected to find the PWA launcher")
	}
	if filepath.Base(got.Path) != "chrome-abcdefghijklmnopabcdefghijklmnop-Default.desktop" {
		t.Fatalf("found %q, want the PWA launcher (not the browser entry)", got.Path)
	}
}

func TestFindPWALauncherDirsSkipsNonMatches(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
	}{
		{
			name: "another app",
			file: "other.desktop",
			body: "[Desktop Entry]\nName=Other App\nExec=/usr/bin/google-chrome --app-id=zzz\n",
		},
		{
			// A launcher that is NOT the PWA's (a desktop app with the same name).
			// Starting it would be a second instance: the --app-id signature is what keeps it out.
			name: "the app's own launcher entry",
			file: "io.github.thefranconiancoder.thoughtkeeper.desktop",
			body: "[Desktop Entry]\nType=Application\nName=Thought Keeper\n" +
				"Exec=/usr/bin/thought-keeper --config /home/me/.config/thought-keeper/config.yaml\n",
		},
		{
			name: "hidden",
			file: "hidden.desktop",
			body: "[Desktop Entry]\nName=Thought Keeper\nNoDisplay=true\nExec=/usr/bin/google-chrome --app-id=zzz\n",
		},
		{
			name: "not an app entry",
			file: "link.desktop",
			body: "[Desktop Entry]\nName=Thought Keeper\nType=Link\nURL=https://example.com\n",
		},
		{
			name: "only a localized name",
			file: "de.desktop",
			body: "[Desktop Entry]\nName[de]=Denker\nExec=/usr/bin/google-chrome --app-id=zzz\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDesktop(t, dir, tc.file, tc.body)
			if _, ok := findPWALauncherDirs([]string{dir}, "Thought Keeper"); ok {
				t.Fatalf("%s: found a launcher, want none", tc.name)
			}
		})
	}
}

func TestFindPWALauncherDirsEmptyName(t *testing.T) {
	dir := t.TempDir()
	writeDesktop(t, dir, "chrome.desktop", chromePWALauncher)
	if _, ok := findPWALauncherDirs([]string{dir}, "   "); ok {
		t.Fatal("an empty name must not match anything (the UI is not built into the binary)")
	}
}

func TestFindPWALauncherDirsMissingDir(t *testing.T) {
	if _, ok := findPWALauncherDirs([]string{filepath.Join(t.TempDir(), "nope")}, "Thought Keeper"); ok {
		t.Fatal("a missing directory must yield no launcher, not an error the user sees")
	}
}

func TestParseDesktopEntry(t *testing.T) {
	got, ok := parseDesktopEntry("[Desktop Entry]\nName=Thought Keeper\nExec=/usr/bin/x %U\nNoDisplay=true\n")
	if !ok || got.name != "Thought Keeper" || got.exec != "/usr/bin/x %U" || !got.noDisplay {
		t.Fatalf("parse = %+v ok=%t, want the Desktop Entry group", got, ok)
	}
	// A key outside [Desktop Entry] (here the Actions group) is not the entry's own.
	if _, ok := parseDesktopEntry("[Desktop Action New]\nName=Ignore me\n"); ok {
		t.Fatal("a Name outside [Desktop Entry] must not count")
	}
	// Whitespace around the key/value is tolerated (real files are hand-editable).
	if got, ok := parseDesktopEntry("[Desktop Entry]\n  Name = Thought Keeper \n"); !ok || got.name != "Thought Keeper" {
		t.Fatalf("parse = %+v ok=%t, want the trimmed name", got, ok)
	}
}

func TestIsChromiumPWALauncher(t *testing.T) {
	if !isChromiumPWALauncher("/usr/bin/google-chrome --profile-directory=Default --app-id=abc") {
		t.Error("an --app-id Exec is a PWA launcher")
	}
	if isChromiumPWALauncher("/usr/bin/google-chrome %U") {
		t.Error("the browser's own entry is not a PWA launcher")
	}
}
