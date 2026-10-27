package router

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestRequestLoggerKeepsReadsOutOfInfo(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		status int
		want   slog.Level
	}{
		{name: "successful read", method: http.MethodGet, path: "/api/v1/sources", status: http.StatusOK, want: slog.LevelDebug},
		{name: "failed read", method: http.MethodGet, path: "/api/v1/sources", status: http.StatusInternalServerError, want: slog.LevelInfo},
		{name: "write", method: http.MethodPost, path: "/api/v1/reindex", status: http.StatusOK, want: slog.LevelInfo},
		{name: "missing favicon", method: http.MethodGet, path: "/api/v1/sources/abc/favicon", status: http.StatusNotFound, want: slog.LevelDebug},
		{name: "event stream", method: http.MethodGet, path: "/api/v1/events", status: http.StatusOK, want: slog.LevelInfo},
		{name: "static asset", method: http.MethodGet, path: "/index.html", status: http.StatusOK, want: slog.LevelDebug},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured captureHandler
			logger := slog.New(&captured)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			})

			requestLogger(logger)(next).ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(tc.method, tc.path, nil),
			)

			if len(captured.records) != 1 {
				t.Fatalf("records = %d, want 1", len(captured.records))
			}
			if got := captured.records[0].Level; got != tc.want {
				t.Errorf("level = %s, want %s", got, tc.want)
			}
		})
	}
}
