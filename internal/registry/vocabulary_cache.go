package registry

import (
	"context"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// vocabularyTTL is how old one vocabulary report may be before it is rebuilt behind the reader. The
// report computes the near-duplicate families over a catalogue of ~18k names on every build
// (400–600 ms), and the PWA asks for it whenever the Status lens loads. Serving the last answer and
// re-reading after the answer is honest for a health view: a rename shows up after one TTL, and no
// reader waits for the scan.
const vocabularyTTL = 15 * time.Second

type cachedVocabulary struct {
	cache *reportCache[[]partialentities.VocabularyHealth]
}

func newCachedVocabulary(uc usecases.Vocabulary) *cachedVocabulary {
	return &cachedVocabulary{cache: &reportCache[[]partialentities.VocabularyHealth]{
		compute: uc.Report,
		ttl:     vocabularyTTL,
	}}
}

func (c *cachedVocabulary) Report(ctx context.Context) ([]partialentities.VocabularyHealth, error) {
	return c.cache.Get(ctx)
}

// Warm builds the first report (see reportCache.Warm).
func (c *cachedVocabulary) Warm() { c.cache.Warm() }
