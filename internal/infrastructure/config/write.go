package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Patch is a set of dotted YAML keys and their new values, as the Config overlay sends them. Only
// the allowlisted knobs (the curated set and Advanced) are accepted.
type Patch map[string]any

// Write applies a patch to the YAML file WITHOUT resolving anything: the file is decoded as a node
// tree, only the patched scalars are replaced (comments on the surrounding nodes survive), the result
// is validated by loading it, and only then does it replace the original. The previous bytes stay
// next to it as <file>.bak.
//
// A missing file is created. A broken file refuses the patch: editing a file that no longer parses
// would hide the problem it says it has.
func Write(path string, patch Patch) (Loaded, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = expandHome(path)
	current, err := LoadFull(path)
	if err != nil {
		return Loaded{}, err
	}
	if len(patch) == 0 {
		return current, nil
	}

	kinds := fieldKinds(current.Config)
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !Writable(key) {
			return current, fmt.Errorf("%w: %s", ErrNotWritable, key)
		}
		if current.Env[key] != "" {
			return current, fmt.Errorf("%w: %s is set by %s", ErrNotWritable, key, current.Env[key])
		}
		if kinds[key] == "" {
			return current, fmt.Errorf("%w: %s is not a known knob", ErrInvalidValue, key)
		}
		if err := validatePatchValue(key, patch[key]); err != nil {
			return current, err
		}
	}

	var doc yaml.Node
	if data, readErr := os.ReadFile(path); readErr == nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return current, fmt.Errorf("parse config: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return current, fmt.Errorf("read config: %w", readErr)
	}
	root, err := documentRoot(&doc)
	if err != nil {
		return current, err
	}
	for _, key := range keys {
		if err := setValue(root, key, patch[key], kinds[key]); err != nil {
			return current, err
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return current, fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return current, fmt.Errorf("encode config: %w", err)
	}
	if err := replaceFile(path, buf.Bytes(), current.Exists); err != nil {
		return current, err
	}
	return LoadFull(path)
}

// validatePatchValue checks the value BEFORE applyDefaults can mask it, with the same rule the whole
// config is validated by. A zero worker count, for example, would otherwise be silently replaced by
// the default and the file would say one thing while the app does another.
func validatePatchValue(key string, value any) error {
	if problem := keyProblem(key, value); problem != "" {
		return fmt.Errorf("%w: %s", ErrInvalidValue, problem)
	}
	return nil
}

// fieldKinds names the kind of every knob in the running config; the writer coerces a JSON value by
// it. All writable keys exist in the defaults, so a missing key is an unknown knob.
func fieldKinds(cfg Config) map[string]string {
	out := map[string]string{}
	for _, section := range sectionsOf(cfg) {
		for _, field := range section.Fields {
			out[field.Key] = field.Kind
		}
	}
	return out
}

// documentRoot returns the mapping a document edits. An empty file (or a file of comments only) gets
// a fresh mapping; anything else that is not a mapping is refused instead of silently replaced.
func documentRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%w: the config is not a YAML mapping", ErrInvalidValue)
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root.Kind, root.Tag, root.Value = yaml.MappingNode, "!!map", ""
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: the config is not a YAML mapping", ErrInvalidValue)
	}
	return root, nil
}

// setValue replaces (or creates) one dotted key. Comments attached to the existing VALUE node are
// carried over; the key node and every surrounding node stay untouched.
func setValue(root *yaml.Node, key string, value any, kind string) error {
	parts := strings.Split(key, ".")
	node := root
	for _, part := range parts[:len(parts)-1] {
		next := mappingValue(node, part)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendPair(node, part, next)
		}
		if next.Kind != yaml.MappingNode {
			return fmt.Errorf("%w: %s is not a mapping", ErrInvalidValue, part)
		}
		node = next
	}
	replacement, err := valueNode(value, kind)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidValue, key, err)
	}
	leaf := parts[len(parts)-1]
	if existing := mappingValue(node, leaf); existing != nil {
		replacement.HeadComment = existing.HeadComment
		replacement.LineComment = existing.LineComment
		replacement.FootComment = existing.FootComment
		*existing = *replacement
		return nil
	}
	appendPair(node, leaf, replacement)
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func appendPair(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// valueNode turns a JSON value into a YAML scalar or sequence of the kind the knob expects.
func valueNode(value any, kind string) (*yaml.Node, error) {
	switch kind {
	case "bool":
		b, err := asBool(value)
		if err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}, nil
	case "int":
		n, err := asInt(value)
		if err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(n, 10)}, nil
	case "float":
		f, err := asFloat(value)
		if err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(f, 'g', -1, 64)}, nil
	case "list":
		items, err := asStrings(value)
		if err != nil {
			return nil, err
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range items {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item})
		}
		return seq, nil
	default:
		s, err := asString(value)
		if err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}, nil
	}
}

func asBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "on", "1":
			return true, nil
		case "false", "off", "0":
			return false, nil
		}
	}
	return false, fmt.Errorf("want a boolean")
}

func asInt(value any) (int64, error) {
	switch v := value.(type) {
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("want an integer")
		}
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("want an integer")
		}
		return n, nil
	}
	return 0, fmt.Errorf("want an integer")
}

func asFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("want a number")
		}
		return f, nil
	}
	return 0, fmt.Errorf("want a number")
}

func asString(value any) (string, error) {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s), nil
	}
	return "", fmt.Errorf("want a string")
}

func asStrings(value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("want a list of strings")
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case string:
		out := make([]string, 0, 4)
		for item := range strings.SplitSeq(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("want a list of strings")
}

// replaceFile validates the new bytes in a temp file next to the original, keeps the old bytes as
// <file>.bak, and only then renames. A validation failure leaves the original untouched.
func replaceFile(path string, data []byte, exists bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() //nolint:errcheck // cleanup; a rename already removed it
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close() //nolint:errcheck // the write already failed; the temp file is removed
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() //nolint:errcheck // the write already failed; the temp file is removed
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	loaded, err := LoadFull(tmpName)
	if err != nil {
		return err
	}
	if err := Validate(loaded.Config); err != nil {
		return err
	}
	if exists {
		old, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read config for backup: %w", err)
		}
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("write config backup: %w", err)
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
