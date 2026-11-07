package payloads

import (
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// TestSourceFromPartialCarriesTheAttachment: the source page decides between "Attach snapshot…" and
// "Replace snapshot… / Try the URL again" from the wire, never from the capture detail string.
func TestSourceFromPartialCarriesTheAttachment(t *testing.T) {
	wire := SourceFromPartial(partialentities.PartialSource{
		Kind: "url", Attached: true, AttachedFrom: "page.html",
	})
	if !wire.Attached || wire.AttachedFrom != "page.html" {
		t.Fatalf("wire = %+v, want the attachment", wire)
	}
}

// TestSourceImportedAtKeepsTheSubSecondPart: the reader's seen watermark is an exact imported_at, and
// a source that arrived in the same second as the last acknowledgment must still be able to count as
// new. Rounding the arrival to the second made it look OLDER than the watermark.
func TestSourceImportedAtKeepsTheSubSecondPart(t *testing.T) {
	at := time.Date(2026, 11, 7, 15, 28, 23, 512_000_000, time.UTC)
	wire := SourceFromPartial(partialentities.PartialSource{ImportedAt: at})
	if wire.ImportedAt != "2026-11-07T15:28:23.512Z" {
		t.Fatalf("imported_at = %q, want the millisecond kept", wire.ImportedAt)
	}
}
