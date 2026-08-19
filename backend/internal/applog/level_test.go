package applog

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw   string
		level slog.Level
		ok    bool
	}{
		{"debug", slog.LevelDebug, true},
		{"DEBUG", slog.LevelDebug, true},
		{" info ", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"ERROR", slog.LevelError, true},
		{"", 0, false},
		{"   ", 0, false},
		{"trace", 0, false},
		{"off", 0, false},
		{"0", 0, false},
	}
	for _, tt := range tests {
		level, ok := ParseLevel(tt.raw)
		if ok != tt.ok {
			t.Errorf("ParseLevel(%q) ok=%v, want %v", tt.raw, ok, tt.ok)
		}
		if ok && level != tt.level {
			t.Errorf("ParseLevel(%q) level=%v, want %v", tt.raw, level, tt.level)
		}
	}
}
