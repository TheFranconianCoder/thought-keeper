// Package gateways holds adapters for external systems (processes, HTTP, LLMs).
package gateways

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ExecRunner is the ToolRunner implementation backed by os/exec. It never keeps a
// process alive between calls.
type ExecRunner struct{}

// Available reports whether name resolves on PATH.
func (ExecRunner) Available(name string) bool {
	if name == "" {
		return false
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// Run executes name with args and the given stdin, returning stdout.
func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, errb.String())
	}
	return out.Bytes(), nil
}
