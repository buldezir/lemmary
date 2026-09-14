package applog

import (
	"log/slog"
	"os"
	"strings"
)

const EnvLogLevel = "LOG_LEVEL"

// ParseLevel reports false for empty and unknown values, meaning no stdout tee.
func ParseLevel(raw string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return 0, false
	}
}

func levelFromEnv() (slog.Level, bool) {
	return ParseLevel(os.Getenv(EnvLogLevel))
}
