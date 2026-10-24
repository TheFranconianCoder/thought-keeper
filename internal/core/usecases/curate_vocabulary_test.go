package usecases

import (
	"context"
	"fmt"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// sequenceGateway answers each call from a script, so a retry can be tested without a model.
type sequenceGateway struct {
	answers []string
	calls   int
}

func (g *sequenceGateway) Complete(_ context.Context, _ interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	if g.calls >= len(g.answers) {
		return interfaces.AgentResponse{}, fmt.Errorf("unexpected extra call")
	}
	answer := g.answers[g.calls]
	g.calls++
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: answer}}, nil
}

// TestAskChunkRetriesAnAnswerWithoutJSON: a large catalog may produce prose once; the second,
// stricter question must still be able to finish. The retry is not a second curation, it is the same
// question asked again with the formatting rule repeated.
func TestAskChunkRetriesAnAnswerWithoutJSON(t *testing.T) {
	gw := &sequenceGateway{answers: []string{
		"I kept every name as it is.",
		`{"canonical":[],"mapping":{"cache-invalidation":"caching"}}`,
	}}
	uc := CurateVocabulary{Gateway: gw, Model: "test"}
	got := uc.askChunk(context.Background(), []nameCount{{name: "cache-invalidation", uses: 2}})
	if got.err != nil {
		t.Fatalf("askChunk: %v", got.err)
	}
	if got.mapping["cache-invalidation"] != "caching" {
		t.Fatalf("mapping = %v, want the retry's answer", got.mapping)
	}
	if gw.calls != 2 {
		t.Fatalf("calls = %d, want one strict retry", gw.calls)
	}
}

// TestChunkByFamilyNeverExceedsTheLimit: a family larger than one prompt is split, while different
// families are still kept together where they fit.
func TestChunkByFamilyNeverExceedsTheLimit(t *testing.T) {
	var chosen []nameCount
	for i := range 2500 {
		chosen = append(chosen, nameCount{name: fmt.Sprintf("same-%d", i), uses: 1, fam: "same"})
	}
	chunks := chunkByFamily(chosen, 1000, 64)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
		if len(chunk) > 1000 {
			t.Fatalf("chunk has %d names, above the 1000 limit", len(chunk))
		}
	}
	if total != len(chosen) {
		t.Fatalf("chunks hold %d names, want %d", total, len(chosen))
	}
}
