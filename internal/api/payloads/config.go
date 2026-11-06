package payloads

// Config is the running configuration as GET /api/v1/config reports it. It is a READ of the process,
// not of the file: the values are the ones in effect, and Changed says whether the file on disk says
// something else.
type Config struct {
	// Path is the resolved file (the default location when the app was started without --config).
	Path string `json:"path"`
	// Exists is false when the app runs on the code's defaults.
	Exists bool `json:"exists"`
	// Changed says the file no longer matches what this process runs.
	Changed bool `json:"changed"`
	// Error is the file's read or parse error; the running values are still listed.
	Error    string          `json:"error,omitempty"`
	Sections []ConfigSection `json:"sections"`
}

// ConfigSection is one YAML block in the struct's order.
type ConfigSection struct {
	Name   string        `json:"name"`
	Fields []ConfigField `json:"fields"`
}

// ConfigField is one knob with its origin and its apply behaviour.
type ConfigField struct {
	// Key is the dotted YAML path.
	Key string `json:"key"`
	// Kind is string, bool, int, float, duration, list, map or path.
	Kind string `json:"kind"`
	// Value is the effective value: null for a credential key, the header NAMES for a header map.
	Value any `json:"value"`
	// Resolved is the absolute path a path or tool field actually uses (the binary found on PATH, or
	// the vault/index directory). Empty when a tool is not found.
	Resolved string `json:"resolved,omitempty"`
	// Lookup is the name a tool field searches on PATH; it names what is missing when Resolved is
	// empty. An empty browser.path looks up lightpanda.
	Lookup string `json:"lookup,omitempty"`
	// Source is file, environment or default.
	Source string `json:"source"`
	// Env names the variable that decided an environment value.
	Env string `json:"env,omitempty"`
	// Secret means the value never leaves the process; Set says whether it has one.
	Secret bool `json:"secret,omitempty"`
	Set    bool `json:"set,omitempty"`
	// Restart means a change applies only after a new process.
	Restart bool `json:"restart"`
	// Writable is false in part 1: the overlay reads.
	Writable bool `json:"writable"`
	// Options lists the accepted values of an enumeration.
	Options []string `json:"options,omitempty"`
}

// ConfigWrite is one patch from the overlay: only the keys the reader changed, with the values in
// their JSON shapes (string, boolean, number, list).
type ConfigWrite struct {
	Values map[string]any `json:"values"`
}

// ConfigWriteResult answers a patch. The file is written synchronously; the apply runs in-process and
// is queued behind the gate, so the running view may still be the old one when this is returned.
type ConfigWriteResult struct {
	Queued bool   `json:"queued"`
	Config Config `json:"config"`
}
