package interfaces

import (
	"context"
	"time"
)

// SourceLogEvent is one line in a source's log. Everything that happens to a source —
// intake, extraction, OCR, tree building, every agent turn's steps, the settle, repairs —
// is recorded where the source can be found, so one document can be traced end to end
// without digging through the global log.
type SourceLogEvent struct {
	At      time.Time
	RunID   string
	Phase   string // capture|outline|distill|compile|settle|clean|reindex|ocr|furniture|title
	Level   string // info|warn|error
	Message string
	Fields  map[string]any
}

// SourceLog appends events to per-source logs and reads them back. The log is derived
// state: it lives with the index, not in the vault.
type SourceLog interface {
	Append(ctx context.Context, hash string, e SourceLogEvent)
	Read(ctx context.Context, hash string) (string, error)
	// Remove drops a source's log (rotated file included). A RESET throws the derivation away, and
	// the log of a derivation that no longer exists is not history — it is noise that makes the
	// next run's story unreadable. Deleting a source is different: there the log is the only trace
	// left, so it stays.
	Remove(ctx context.Context, hash string) error
}
