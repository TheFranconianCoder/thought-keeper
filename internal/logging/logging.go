// Package logging configures the process-wide slog logger.
package logging

import (
	"log/slog"
	"os"
)

// Setup installs a text slog handler on stderr and returns the logger.
func Setup(level slog.Level) *slog.Logger {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}
