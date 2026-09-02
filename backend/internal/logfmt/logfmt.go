// Package logfmt holds the conventions a slog attribute in this app has to
// follow to survive being persisted by PocketBase.
package logfmt

import (
	"log/slog"
	"time"
)

// Duration is a duration-valued slog attribute, spelled like slog.String
// because that is what it has to become.
//
// It has to be a string. PocketBase persists a log record by marshalling its
// attributes to JSON, and since v0.40 moved to encoding/json/v2 a
// time.Duration has no default representation there. The marshal fails, and
// PocketBase drops the whole record -- message, level, every other attribute --
// with "Failed to write log ... no default representation". So passing a
// time.Duration does not merely format it oddly, it deletes the log line: the
// AI, OCR and pipeline timings all vanished from the logs table under v0.40
// while still printing to stderr, which is a quiet way to lose them.
//
// Millisecond rounding is what the time.Duration attributes this replaced
// rendered with, and as much precision as this app's logs have ever wanted.
func Duration(key string, d time.Duration) slog.Attr {
	return slog.String(key, d.Round(time.Millisecond).String())
}
