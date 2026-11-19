package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// TestRefreshUrlsReportsTheDeferredChanges: `changed` counts every page whose text differs, and
// `deferred` the subset the vault did NOT store because the drift is below the threshold. A reader
// who sees "changed 12" must be able to see that eleven of them cost nothing.
func TestRefreshUrlsReportsTheDeferredChanges(t *testing.T) {
	h := URLHandler{
		Refresh: func(context.Context) (usecases.RefreshResult, error) {
			return usecases.RefreshResult{}, nil
		},
		RefreshAll: func(context.Context) (usecases.RefreshResult, error) {
			return usecases.RefreshResult{
				Checked: 3, Changed: 2, Material: 1, Deferred: 1,
				DeferredHashes: []string{"aa"},
			}, nil
		},
	}

	rec := httptest.NewRecorder()
	h.Post(rec, httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/refresh-urls", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := body["deferred"]; got != float64(1) {
		t.Errorf("deferred = %v, want 1", got)
	}
	if got := body["changed"]; got != float64(2) {
		t.Errorf("changed = %v, want 2", got)
	}
}
