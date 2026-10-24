package registry

import (
	"context"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// vocabularyTTL is how long one vocabulary report serves every reader. The report computes the
// near-duplicate families over a catalogue of ~18k names on every call (400–600 ms), and the PWA
// asks for it whenever the status lens loads. A short cache is honest for a health view: a rename
// shows up after one TTL.
const vocabularyTTL = 15 * time.Second

type cachedVocabulary struct {
	uc usecases.VocabularyReport

	mu    sync.Mutex
	at    time.Time
	res   []partialentities.VocabularyHealth
	valid bool
}

func (c *cachedVocabulary) Execute(ctx context.Context) ([]partialentities.VocabularyHealth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && time.Since(c.at) < vocabularyTTL {
		return c.res, nil
	}
	res, err := c.uc.Execute(ctx)
	if err != nil {
		return res, err
	}
	c.res, c.at, c.valid = res, time.Now(), true
	return res, nil
}
