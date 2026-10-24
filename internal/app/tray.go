package app

import (
	"os"

	"github.com/gogpu/systray"

	"github.com/TheFranconianCoder/thought-keeper/internal/version"
)

// runTray shows the tray icon and blocks on the platform message loop. It returns
// when the tray is removed (Quit, or the quit channel closes).
func (a *App) runTray() {
	t := systray.New()

	menu := systray.NewMenu()
	menu.Add("Open Thought Keeper", func() { a.openURL() })
	menu.Add("Pause watcher", func() { a.toggleWatcher() })
	menu.AddSeparator()
	menu.Add("Quit", func() { a.requestQuit() })

	t.SetIcon(trayIcon()).
		SetTooltip("Thought Keeper " + version.String()).
		SetMenu(menu)
	t.OnClick(func() { a.openURL() })
	t.Show()

	go func() {
		<-a.quit
		a.shutdown()
		t.Remove()
		os.Exit(0)
	}()

	if err := t.Run(); err != nil {
		a.log.Warn("tray stopped", "err", err)
	}
}
