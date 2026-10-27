package markdown_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	mdcodec "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/markdown"
)

func TestAtomCodecRoundTrip(t *testing.T) {
	a := entities.Atom{
		ID:        ulid.Make(),
		Topic:     "caching",
		Claim:     "Caching is only safe when the key covers every input.",
		Body:      "If two inputs share a key, one value is wrong. See `[[Other Atom]]`.",
		Type:      entities.AtomClaim,
		Status:    entities.AtomActive,
		Sources:   []entities.AtomSource{{Hash: strings.Repeat("a", 64), NodeID: "0.1", OCR: true}},
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(),
		UpdatedAt: time.UnixMilli(1_700_000_500_000).UTC(),
	}

	data, err := mdcodec.EncodeAtom(a)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(string(data), "---\n") {
		t.Fatalf("no frontmatter:\n%s", data)
	}

	got, err := mdcodec.DecodeAtom(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != a.ID || got.Claim != a.Claim || got.Topic != a.Topic {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.Type != entities.AtomClaim || got.Status != entities.AtomActive {
		t.Errorf("type/status = %s/%s", got.Type, got.Status)
	}
	if len(got.Sources) != 1 || got.Sources[0].NodeID != "0.1" || !got.Sources[0].OCR {
		t.Errorf("sources = %+v", got.Sources)
	}
	// A bracket pair is prose. It is no longer decoded as a link, and the body keeps it verbatim.
	if !strings.Contains(got.Body, "`[[Other Atom]]`") {
		t.Errorf("body = %q", got.Body)
	}
}

func TestDecodeAtomRejectsMissingFrontmatter(t *testing.T) {
	if _, err := mdcodec.DecodeAtom([]byte("just a body")); err == nil {
		t.Fatal("expected an error for missing frontmatter")
	}
}
