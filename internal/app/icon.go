package app

import _ "embed"

// trayIconPNG is the studio's Thought Keeper pictogram, rendered to PNG (32x32)
// from design/pictograms/thought-keeper.svg. See AGENTS.md for regeneration.
//
//go:embed icon.png
var trayIconPNG []byte

// trayIcon returns the tray icon bytes.
func trayIcon() []byte { return trayIconPNG }
