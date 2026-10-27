package payloads

import (
	"testing"

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
