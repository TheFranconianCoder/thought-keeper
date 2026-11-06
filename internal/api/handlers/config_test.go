package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
)

type stubConfig struct {
	view payloads.Config
}

func (s stubConfig) Config() payloads.Config { return s.view }

func TestConfigGet(t *testing.T) {
	want := payloads.Config{
		Path:    "/tmp/config.yaml",
		Exists:  true,
		Changed: true,
		Sections: []payloads.ConfigSection{{
			Name: "language",
			Fields: []payloads.ConfigField{{
				Key: "language.claims", Kind: "string", Value: "de", Source: "file",
			}},
		}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/config", nil)
	ConfigHandler{Provider: stubConfig{view: want}}.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got payloads.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != want.Path || !got.Changed || len(got.Sections) != 1 {
		t.Errorf("view = %+v, want the provider's answer", got)
	}
	if got.Sections[0].Fields[0].Key != "language.claims" {
		t.Errorf("field = %+v, want language.claims", got.Sections[0].Fields[0])
	}
}

func TestConfigWithoutProviderIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/config", nil)
	ConfigHandler{}.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
