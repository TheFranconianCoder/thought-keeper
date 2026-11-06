package app

import (
	"context"
	"errors"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
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
	view := config.Describe(a.conf(), a.cfgPath)
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

// WriteConfig writes a patch to the YAML and queues the in-process apply. The file is the truth and
// is written first; the running configuration follows as soon as the exclusive gate is free. The
// answer therefore says "queued" and carries the running view (which still shows changed=true until
// the apply lands).
func (a *App) WriteConfig(in payloads.ConfigWrite) (payloads.ConfigWriteResult, error) {
	if len(in.Values) == 0 {
		return payloads.ConfigWriteResult{Config: a.Config()}, nil
	}
	a.configWriteMu.Lock()
	loaded, err := config.Write(a.cfgPath, config.Patch(in.Values))
	a.configWriteMu.Unlock()
	if err != nil {
		switch {
		case errors.Is(err, config.ErrNotWritable):
			return payloads.ConfigWriteResult{}, types.New("invalid_body", err.Error(), nil)
		case errors.Is(err, config.ErrInvalidValue):
			return payloads.ConfigWriteResult{}, types.New("unparsed", err.Error(), nil)
		}
		return payloads.ConfigWriteResult{}, err
	}
	go a.applyConfig(loaded)
	return payloads.ConfigWriteResult{Queued: true, Config: a.Config()}, nil
}

// applyConfig swaps the wiring for a new configuration. It is EXCLUSIVE: document steps and the
// cluster pass are not running while the old Deps is replaced, and the in-flight HTTP requests keep
// the handler they started with. The new Deps is immutable; nothing is mutated after it is published.
func (a *App) applyConfig(loaded config.Loaded) {
	wasAuto := a.conf().Pipeline.Auto
	err := a.ExclusiveWrite(context.Background(), "config apply", func(context.Context) error {
		a.stopRunWorkers()
		reg := a.buildWiring(loaded.Config)
		reg.ClusterPass.Progress = a.setClusterProgress
		a.setConfig(loaded.Config)
		a.depsPtr.Store(&reg)
		a.handler.Store(reg.Handler)
		a.startRunWorkers()
		a.applyMemoryLimit(loaded.Config.App.MemoryLimitMB)
		return nil
	})
	if err != nil {
		a.log.Warn("config apply failed", "err", err)
		if a.hub != nil {
			a.hub.Publish("config", map[string]any{"error": err.Error()})
		}
		return
	}
	if !wasAuto && loaded.Config.Pipeline.Auto {
		// A file dropped while the watcher was off is still in the inbox; enable it and take it now.
		go a.scanInbox(loaded.Config.InboxDir())
	}
	if a.hub != nil {
		a.hub.Publish("config", map[string]any{"applied": true})
	}
	a.publishPipeline()
}

// ReloadConfig applies the file as it is on disk: the SSH-edit path. The write endpoint is not
// involved, so the running config and the file converge without a restart.
func (a *App) ReloadConfig() (payloads.ConfigWriteResult, error) {
	loaded, err := config.LoadFull(a.cfgPath)
	if err != nil {
		return payloads.ConfigWriteResult{}, types.New("unparsed", err.Error(), nil)
	}
	if err := config.Validate(loaded.Config); err != nil {
		return payloads.ConfigWriteResult{}, types.New("unparsed", err.Error(), nil)
	}
	go a.applyConfig(loaded)
	return payloads.ConfigWriteResult{Queued: true, Config: a.Config()}, nil
}
