package config

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loaded is the configuration as it was READ: the effective values, the file they came from, and
// the two provenances a value can have beyond the code's defaults — the file itself and the
// environment (which wins over the file for the OpenAI knobs, and only fills in the key and the base
// URL when the file leaves them empty).
type Loaded struct {
	Config Config
	// Path is the resolved file: DefaultPath when the caller passed an empty path, with ~ expanded.
	Path string
	// Exists is false when the app runs on the code's defaults (a missing file is not an error).
	Exists bool
	// Set holds the dotted keys the FILE declared, e.g. "language.claims". A key that is absent was
	// filled from a default (or from the environment).
	Set map[string]bool
	// Env maps a dotted key the ENVIRONMENT decided to the variable that decided it.
	Env map[string]string
}

// View is the running configuration as the Config overlay shows it: the values in effect, plus where
// each one came from and whether changing the file would change it at once or only after a restart.
type View struct {
	Path   string
	Exists bool
	// Changed says the file on disk (or its absence) no longer matches what this process runs. It is
	// the signal behind "the file says something else than the app is doing".
	Changed bool
	// Error is the file's read or parse error. The running values are still listed: they are still
	// what the process uses.
	Error    string
	Sections []Section
}

// Section is one YAML block, in the order of the Config struct (= the order of config.example.yaml).
type Section struct {
	Name   string
	Fields []Field
}

// Field is one knob.
type Field struct {
	// Key is the dotted YAML path: "capture.refresh_interval", "vault.git.ignore_derived".
	Key string
	// Kind is the value's shape for the UI: string, bool, int, float, duration, list, map or path.
	Kind string
	// Value is the effective value. A credential never travels: a key reports only Set, and a header
	// map reports its header NAMES and never their values.
	Value any
	// Resolved is the absolute path the process actually uses for a "path" or "tool" field: the
	// vault/index directory as it exists, or the binary found on PATH. Empty when a tool is not
	// found. A bare "pdftotext" in the config does not answer "which binary runs"; this does.
	Resolved string
	// Lookup is the name a tool field searches on PATH. It is the configured value, except for an
	// empty browser.path: that means the engine's own name, lightpanda. It names what is missing
	// when Resolved is empty.
	Lookup string
	// Source is "file", "environment" or "default".
	Source string
	// Env names the variable that decided the value when Source is "environment".
	Env string
	// Secret means the value must never leave the process: only Set says whether it has one.
	Secret bool
	Set    bool
	// Restart means a change applies only after a new process. Live knobs are the exception.
	Restart bool
	// Writable is always false in part 1. Part 2 opens the curated allowlist.
	Writable bool
	// Options lists the accepted values when the knob is an enumeration.
	Options []string
}

// liveKeys apply without a new process today: the scheduler reads them from the running
// configuration, everything else was captured when the wiring was built.
var liveKeys = map[string]bool{
	"pipeline.auto_distill": true,
	"pipeline.debounce":     true,
}

var optionsKeys = map[string][]string{
	"browser.wait_until":              {"load", "networkidle", "done"},
	"openai.reasoning_effort_compile": {"minimal", "low", "medium", "high"},
	"openai.reasoning_effort_distill": {"minimal", "low", "medium", "high"},
	"openai.reasoning_effort_reason":  {"minimal", "low", "medium", "high"},
}

// Describe reads the file at path and compares it with the configuration the process is running. The
// VALUES it returns are the running ones — the question is "what is this instance set to", and the
// drift is a separate fact (Changed). A file that cannot be read is reported, not hidden: a typo made
// over SSH must be visible, while the running values stay readable.
func Describe(running Config, path string) View {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	view := View{Path: path, Sections: sectionsOf(running)}
	applyResolved(view.Sections, running)
	loaded, err := LoadFull(path)
	view.Path = loaded.Path
	view.Exists = loaded.Exists
	if err != nil {
		// The file cannot be read: the running values stay, but their origin cannot be attributed to
		// what is on disk now. The overlay shows the error and leaves the origin blank.
		view.Error = err.Error()
		return view
	}
	view.Changed = !reflect.DeepEqual(running, loaded.Config)
	applyOrigins(view.Sections, loaded.Set, loaded.Env)
	return view
}

// applyResolved fills the absolute path the process uses for path and tool fields. It depends only
// on the running configuration, so it is done before the file is read: a broken file must not hide
// which binary is actually running.
func applyResolved(sections []Section, cfg Config) {
	for i := range sections {
		for j := range sections[i].Fields {
			field := &sections[i].Fields[j]
			if field.Kind != "path" && field.Kind != "tool" {
				continue
			}
			if field.Kind == "tool" {
				field.Lookup = toolLookup(field.Key, cfg)
			}
			field.Resolved = resolvedPath(field.Key, cfg)
		}
	}
}

// resolvedPath answers the question a bare name cannot: WHERE is this? A vault or index directory is
// joined and made absolute; a tool is looked up on the process's PATH (an absolute configured path is
// used as it is). An empty result means "not found", not "default".
func resolvedPath(key string, cfg Config) string {
	switch key {
	case "vault.root":
		return absPath(cfg.VaultRoot())
	case "vault.sources":
		return absPath(cfg.SourcesDir())
	case "vault.inbox":
		return absPath(cfg.InboxDir())
	case "vault.atoms":
		return absPath(cfg.AtomsDir())
	case "vault.wiki":
		return absPath(cfg.WikiDir())
	case "vault.clusters":
		return absPath(cfg.ClustersDir())
	case "vault.asks":
		return absPath(cfg.AsksDir())
	case "vault.commands":
		return absPath(cfg.CommandsDir())
	case "vault.vocabulary":
		return absPath(cfg.VocabularyDir())
	case "vault.interests":
		return absPath(cfg.InterestsDir())
	case "vault.trash":
		return absPath(filepath.Join(cfg.VaultRoot(), cfg.Vault.Trash))
	case "index.path":
		return absPath(cfg.IndexDB())
	case "index.favicons":
		return absPath(cfg.FaviconsDir())
	case "pdf.text_tool":
		return lookTool(cfg.PDF.TextTool)
	case "ocr.rasterizer":
		return lookTool(cfg.OCR.Rasterizer)
	case "ocr.tesseract":
		return lookTool(cfg.OCR.Tesseract)
	case "browser.path":
		// An empty path is not "nowhere": it is the engine's own name, looked up on PATH.
		if strings.TrimSpace(cfg.Browser.Path) == "" {
			return lookTool("lightpanda")
		}
		return lookTool(cfg.Browser.Path)
	}
	return ""
}

// toolLookup names what a tool field asks PATH for. An empty browser.path is not "no tool": it is
// the engine's own name, which is why the overlay can say "lightpanda · not found" instead of a bare
// "not found" that leaves the reader guessing.
func toolLookup(key string, cfg Config) string {
	switch key {
	case "pdf.text_tool":
		return strings.TrimSpace(cfg.PDF.TextTool)
	case "ocr.rasterizer":
		return strings.TrimSpace(cfg.OCR.Rasterizer)
	case "ocr.tesseract":
		return strings.TrimSpace(cfg.OCR.Tesseract)
	case "browser.path":
		if strings.TrimSpace(cfg.Browser.Path) == "" {
			return "lightpanda"
		}
		return strings.TrimSpace(cfg.Browser.Path)
	}
	return ""
}

func lookTool(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return absPath(path)
}

func absPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// applyOrigins labels each field with the place its value came from. The file is read once per
// request, so a value edited over SSH is labelled correctly the moment it is saved.
func applyOrigins(sections []Section, set map[string]bool, env map[string]string) {
	for i := range sections {
		for j := range sections[i].Fields {
			field := &sections[i].Fields[j]
			switch {
			case env[field.Key] != "":
				field.Source, field.Env = "environment", env[field.Key]
			case set[field.Key]:
				field.Source = "file"
			default:
				field.Source = "default"
			}
		}
	}
}

// sectionsOf marshals the effective configuration and walks it in struct order. The value types come
// from the YAML nodes, so the overlay never needs a second table that could drift from the struct.
func sectionsOf(c Config) []Section {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	sections := make([]Section, 0, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		section := Section{Name: root.Content[i].Value}
		collectFields(root.Content[i+1], section.Name, &section.Fields)
		sections = append(sections, section)
	}
	return sections
}

// collectFields flattens nested mappings (vault.git) into dotted keys, except for openai.headers: its
// VALUES are credentials, so it stays one field and the overlay only learns which header names exist.
func collectFields(node *yaml.Node, prefix string, out *[]Field) {
	if node.Kind == yaml.MappingNode && prefix != "openai.headers" {
		for i := 0; i+1 < len(node.Content); i += 2 {
			collectFields(node.Content[i+1], prefix+"."+node.Content[i].Value, out)
		}
		return
	}
	field := Field{
		Key:      prefix,
		Kind:     kindOf(prefix, node),
		Restart:  !liveKeys[prefix],
		Writable: false,
		Options:  optionsKeys[prefix],
	}
	if prefix == "openai.api_key" {
		field.Value = nil
		field.Secret = true
		field.Set = strings.TrimSpace(node.Value) != ""
		*out = append(*out, field)
		return
	}
	if prefix == "openai.headers" {
		var headers map[string]string
		if err := node.Decode(&headers); err == nil {
			names := make([]string, 0, len(headers))
			for name := range headers {
				names = append(names, name)
			}
			sort.Strings(names)
			field.Value = names
			field.Set = len(names) > 0
		}
		field.Kind = "list"
		field.Secret = true
		*out = append(*out, field)
		return
	}
	var value any
	if err := node.Decode(&value); err != nil {
		value = node.Value
	}
	field.Value = value
	*out = append(*out, field)
}

// kindOf names the value's shape. Durations and paths are strings in the YAML either way; the name is
// what lets the overlay render them as a duration or in mono without guessing from the string.
func kindOf(key string, node *yaml.Node) string {
	switch {
	case isDurationKey(key):
		return "duration"
	case isToolKey(key):
		return "tool"
	case isPathKey(key):
		return "path"
	}
	switch node.Tag {
	case "!!bool":
		return "bool"
	case "!!int":
		return "int"
	case "!!float":
		return "float"
	case "!!seq":
		return "list"
	case "!!map":
		return "map"
	}
	return "string"
}

var durationKeys = map[string]bool{
	"pipeline.run_timeout":     true,
	"pipeline.cluster_timeout": true,
	"openai.timeout":           true,
	"openai.retry_backoff":     true,
	"browser.wait":             true,
	"ocr.timeout":              true,
	"fetch.timeout":            true,
}

func isDurationKey(key string) bool {
	return durationKeys[key] ||
		strings.HasSuffix(key, "_interval") ||
		strings.HasSuffix(key, "_timeout")
}

// toolKeys are binaries, not directories: the overlay shows the absolute path they resolve to, or
// that they were not found. A bare name in the config is an instruction to search PATH, not a path.
var toolKeys = map[string]bool{
	"pdf.text_tool":  true,
	"ocr.rasterizer": true,
	"ocr.tesseract":  true,
	"browser.path":   true,
}

func isToolKey(key string) bool { return toolKeys[key] }

var pathKeys = map[string]bool{
	"index.path":     true,
	"index.favicons": true,
	"browser.path":   true,
	"pdf.text_tool":  true,
	"ocr.rasterizer": true,
	"ocr.tesseract":  true,
}

func isPathKey(key string) bool {
	if pathKeys[key] {
		return true
	}
	// The vault's own directories. vault.git is not a directory: its knobs are switches and strings
	// that belong to the version-control integration, not paths.
	return strings.HasPrefix(key, "vault.") && !strings.HasPrefix(key, "vault.git.")
}

// declaredKeys collects the dotted keys a YAML document declares. It is a second, non-strict read of
// the same bytes the strict loader accepted: only the KEYS matter here, so the values are never
// interpreted twice.
func declaredKeys(text string) map[string]bool {
	out := map[string]bool{}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return out
	}
	walkKeys(&doc, "", out)
	return out
}

func walkKeys(node *yaml.Node, prefix string, out map[string]bool) {
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			walkKeys(child, prefix, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if prefix != "" {
				key = prefix + "." + key
			}
			out[key] = true
			walkKeys(node.Content[i+1], key, out)
		}
	}
}
