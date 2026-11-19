package usecases_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// TestSidecarServesEveryFileASourceHas: the Files view lists entities.SidecarSuffixes() (and the
// rotated older half of the log); the raw endpoint must serve every one of them. The two lists drifted
// apart once: .changes.md was listed with a size, while opening it answered "no file named
// .changes.md" — so the Changes tab showed an error instead of the diff it had just been told existed.
func TestSidecarServesEveryFileASourceHas(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	hash := strings.Repeat("ab", 32)
	idx.m[hash] = entities.Source{Hash: hash, Ext: ".md", Kind: entities.SourceKindURL}
	uc := usecases.Sources{Store: store, Index: idx}

	names := append([]string{}, entities.SidecarSuffixes()...)
	names = append(names, entities.SidecarLog+".1")
	for _, name := range names {
		if err := store.PutRendered(hash, name, []byte("body of "+name)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	for _, name := range names {
		rc, ext, _, err := uc.Sidecar(ctx, hash, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		body, readErr := io.ReadAll(rc)
		_ = rc.Close() //nolint:errcheck // a read-only file that was fully read
		if readErr != nil {
			t.Errorf("%s: read: %v", name, readErr)
		}
		if want := "body of " + name; string(body) != want {
			t.Errorf("%s answered %q, want %q", name, body, want)
		}
		if ext != name {
			t.Errorf("%s reported extension %q", name, ext)
		}
	}

	if _, _, _, err := uc.Sidecar(ctx, hash, ".secrets"); !errors.Is(err, types.ErrNotFound) {
		t.Errorf("an unknown name was not a 404: %v", err)
	}
}
