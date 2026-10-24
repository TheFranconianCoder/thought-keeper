package app

import (
	"os/exec"
	"runtime"
)

// OpenURL hands the address to the operating system's default URL handler
// (xdg-open / open / rundll32). It never invokes a specific browser: whatever the
// user configured opens it — typically the installed PWA, otherwise the default
// browser. The UI enforces a single window itself, so opening the same address again
// does not pile up windows.
func OpenURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// openURL opens this instance's UI: the installed PWA's own window when one can be found, otherwise
// the OS URL handler. `smart_open: false` keeps the plain OS handler.
func (a *App) openURL() {
	url := a.uiURL()
	if !a.cfg.App.SmartOpen {
		a.openWithOSHandler(url)
		return
	}
	via, launcher, err := openUISmart(url, pwaName())
	if err != nil {
		a.log.Warn("could not open the UI", "url", url, "via", via, "err", err)
		return
	}
	a.log.Info("opened the UI", "url", url, "via", via, "launcher", launcher)
}

// openWithOSHandler is the plain path: hand the address to the OS URL handler.
func (a *App) openWithOSHandler(url string) {
	if err := OpenURL(url); err != nil {
		a.log.Warn("could not open the UI", "url", url, "err", err)
		return
	}
	a.log.Info("opened the UI", "url", url, "via", "os")
}
