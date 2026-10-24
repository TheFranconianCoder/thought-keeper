package app

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/TheFranconianCoder/thought-keeper/internal/web"
)

// Smart open: the default shell hands the address to the OS URL handler (`xdg-open` / `open` /
// `rundll32`), and which handler wins is the system's decision — typically the installed PWA,
// otherwise the default browser. When a browser installs the PWA it also writes an OS-level LAUNCHER
// for it, and starting that launcher opens the app's OWN window (its icon, its window class, its
// place in the dock) instead of a URL that some handler has to place.
//
// This is best-effort by design: a browser that wrote no launcher, a machine where the PWA was never
// installed, and every platform not implemented here yield no result, and the caller falls back to
// the OS handler — exactly the behaviour before smart open. It OPENS the PWA; it can never focus or
// close a window that is already open (that window belongs to the browser, not to us).

// pwaLauncher is one OS-level launcher for the installed PWA: a `.desktop` file on Linux, an `.app`
// bundle on macOS, a shortcut on Windows — found by the platform's own scan.
type pwaLauncher struct {
	Path string
}

var (
	pwaNameOnce sync.Once
	pwaNameVal  string
)

// pwaName is the name the installed PWA carries — the name a browser names its launcher after. It is
// read from the app's OWN manifest (the embedded PWA), so there is one source of truth and no second
// copy of the string in the Go code. Empty when the UI is not built into this binary; then there is
// nothing to match against and the OS handler is used.
func pwaName() string {
	pwaNameOnce.Do(func() {
		blob, err := web.Asset("manifest.webmanifest")
		if err != nil {
			return
		}
		var m struct {
			Name      string `json:"name"`
			ShortName string `json:"short_name"`
		}
		if err := json.Unmarshal(blob, &m); err != nil {
			return
		}
		pwaNameVal = strings.TrimSpace(m.Name)
		if pwaNameVal == "" {
			pwaNameVal = strings.TrimSpace(m.ShortName)
		}
	})
	return pwaNameVal
}

// openUISmart opens the UI, preferring the installed PWA's own launcher and falling back to the OS
// URL handler. via is "pwa" or "os"; launcher names the file that was tried, for the log. A missing
// PWA is not a failure — only an OS handler that refuses to start is.
func openUISmart(url, name string) (via, launcher string, err error) {
	if l, ok := findPWALauncher(name); ok {
		if lerr := launchPWALauncher(l); lerr == nil {
			return "pwa", l.Path, nil
		}
		// The launcher was there but would not start: fall back, naming the one that failed.
		return "os", l.Path, OpenURL(url)
	}
	return "os", "", OpenURL(url)
}

// OpenUIAddress opens a running instance's UI at addr, preferring the installed PWA with a fallback
// to the OS URL handler. It never blocks.
func OpenUIAddress(addr string) error {
	_, _, err := openUISmart("http://"+addr, pwaName())
	return err
}
