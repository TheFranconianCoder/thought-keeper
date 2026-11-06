package app

import (
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
)

// resolvedConfigPath names the file the app reads: the --config path, or the default location. The
// Config overlay reports the resolved path, so a reader who never passed --config still knows which
// file to open.
func resolvedConfigPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return config.DefaultPath()
	}
	return path
}

// Config is the running configuration for the Config overlay: the values in effect, where each one
// came from, and whether the file on disk still matches. It reads the file; it never writes it.
func (a *App) Config() payloads.Config {
	view := config.Describe(a.cfg, a.cfgPath)
	out := payloads.Config{
		Path:    view.Path,
		Exists:  view.Exists,
		Changed: view.Changed,
		Error:   view.Error,
	}
	out.Sections = make([]payloads.ConfigSection, 0, len(view.Sections))
	for _, section := range view.Sections {
		fields := make([]payloads.ConfigField, 0, len(section.Fields))
		for _, field := range section.Fields {
			fields = append(fields, payloads.ConfigField{
				Key:      field.Key,
				Kind:     field.Kind,
				Value:    field.Value,
				Resolved: field.Resolved,
				Lookup:   field.Lookup,
				Source:   field.Source,
				Env:      field.Env,
				Secret:   field.Secret,
				Set:      field.Set,
				Restart:  field.Restart,
				Writable: field.Writable,
				Options:  field.Options,
			})
		}
		out.Sections = append(out.Sections, payloads.ConfigSection{Name: section.Name, Fields: fields})
	}
	return out
}
