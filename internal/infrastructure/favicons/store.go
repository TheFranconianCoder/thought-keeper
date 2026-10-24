// Package favicons stores the small icon of a URL source. It is derived data —
// re-fetchable, disposable, kept under the index dir, never in the vault.
package favicons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// Store keeps favicons as <root>/<sha256><ext>.
type Store struct {
	root string
}

var _ interfaces.FaviconStore = (*Store)(nil)

// New returns a store rooted at dir.
func New(dir string) *Store { return &Store{root: dir} }

// Put stores the icon bytes and returns its hash + extension.
func (s *Store) Put(_ context.Context, contentType string, r io.Reader) (interfaces.Favicon, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return interfaces.Favicon{}, fmt.Errorf("favicons: mkdir: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return interfaces.Favicon{}, fmt.Errorf("favicons: read: %w", err)
	}
	if len(b) == 0 {
		return interfaces.Favicon{}, fmt.Errorf("favicons: empty icon")
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	ext := extFor(contentType, b)

	dst := filepath.Join(s.root, hash+ext)
	if _, err := os.Stat(dst); err == nil {
		return interfaces.Favicon{Hash: hash, Ext: ext}, nil // deduplicated
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return interfaces.Favicon{}, fmt.Errorf("favicons: write: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return interfaces.Favicon{}, fmt.Errorf("favicons: rename: %w", err)
	}
	return interfaces.Favicon{Hash: hash, Ext: ext}, nil
}

// Open returns the icon bytes.
func (s *Store) Open(hash, ext string) (io.ReadCloser, error) {
	if !isHash(hash) {
		return nil, fmt.Errorf("favicons: invalid hash")
	}
	return os.Open(filepath.Join(s.root, hash+ext))
}

func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// extFor picks an extension from the content type, falling back to sniffing.
func extFor(contentType string, b []byte) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "svg"):
		return ".svg"
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	case strings.Contains(ct, "icon"), strings.Contains(ct, "vnd.microsoft"):
		return ".ico"
	}
	switch {
	case len(b) >= 4 && b[0] == 0x89 && b[1] == 'P':
		return ".png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8:
		return ".jpg"
	case len(b) >= 5 && strings.HasPrefix(string(b), "<?xml") || strings.Contains(string(b[:min(len(b), 256)]), "<svg"):
		return ".svg"
	default:
		return ".ico"
	}
}
