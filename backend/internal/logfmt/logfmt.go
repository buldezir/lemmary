// Package logfmt holds the conventions a slog attribute in this app has to
// follow to survive being persisted by PocketBase.
package logfmt

import (
	"log/slog"
	"time"
)

// Duration must be a string: PocketBase marshals log attributes with
// encoding/json/v2 since v0.40, where a time.Duration has no default
// representation, so the marshal fails and the whole record is dropped.
func Duration(key string, d time.Duration) slog.Attr {
	return slog.String(key, d.Round(time.Millisecond).String())
}
