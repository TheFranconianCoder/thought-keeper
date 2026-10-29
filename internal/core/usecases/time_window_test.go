package usecases_test

import (
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		raw   string
		want  time.Time
		error bool
	}{
		{name: "empty is no window", raw: "", want: time.Time{}},
		{name: "hours", raw: "24h", want: now.Add(-24 * time.Hour)},
		{name: "days", raw: "7d", want: now.Add(-7 * 24 * time.Hour)},
		{name: "weeks", raw: "1w", want: now.Add(-7 * 24 * time.Hour)},
		{name: "go duration", raw: "168h", want: now.Add(-168 * time.Hour)},
		{
			name: "rfc3339",
			raw:  "2026-10-27T12:00:00+02:00",
			want: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC),
		},
		{name: "zero duration", raw: "0h", error: true},
		{name: "negative duration", raw: "-24h", error: true},
		{name: "garbage", raw: "yesterday", error: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := usecases.ParseSince(tt.raw, now)
			if tt.error {
				if err == nil {
					t.Fatalf("ParseSince(%q) = %v, want an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSince(%q): %v", tt.raw, err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("ParseSince(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
