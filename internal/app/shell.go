// Package app is the desktop composition root (see app.go). This file is the UI shell: the tray owns
// the lifecycle and hands the address to the operating system, which opens the installed PWA
// (otherwise the default browser). Pure Go, no WebView, no CGO — there is one shell, and the PWA is
// the UI.
package app

// runShell shows the UI and blocks until the app quits.
func (a *App) runShell() {
	if a.noOpen {
		// `--no-open`: the service is up and the tray is there, but the address is only logged. The
		// reader opens it themselves — which is what a developer wants, whose browser (or Vite dev
		// server) is already on it.
		a.log.Info("not opening a browser (--no-open)", "url", a.uiURL())
	} else {
		// Hand the address to the OS: the installed PWA (or the default browser) opens. The UI keeps
		// itself single-window.
		go a.openURL()
	}
	a.runTray()
}

// OpenUI opens an already running instance's UI: the installed PWA's own window when one exists,
// otherwise the OS URL handler.
func OpenUI(addr string) error { return OpenUIAddress(addr) }
