package interfaces

import "context"

// ToolRunner runs an external tool ad-hoc: it feeds stdin and returns stdout.
// There is no daemon and no long-running process — the tool is invoked per call
// and gone when it returns.
type ToolRunner interface {
	// Available reports whether the tool is on PATH.
	Available(name string) bool
	// Run executes name with args, writing stdin and returning stdout.
	Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)
}
