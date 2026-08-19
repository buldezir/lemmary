package applog

import (
	"log/slog"
	"os"
	"strings"
)

const EnvLogLevel = "LOG_LEVEL"

// ParseLevel maps a LOG_LEVEL string to a slog level.
// Empty and unknown values return false (no stdout tee).
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
