package logfmt

import (
	"log/slog"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tools/types"
)

// TestDurationSurvivesPocketBaseLogWriter pins the reason this helper exists.
//
// PocketBase stores a log record's attributes in a types.JSONMap[any] and
// persists it by marshalling that map. Under encoding/json/v2, which PocketBase
// moved to in v0.40, a time.Duration in there fails to marshal and the whole
// record is dropped rather than degraded. The second half of this test asserts
// that failure directly, so if a future Go or PocketBase release gives
// time.Duration a default representation the helper can go away on evidence
// rather than on a guess.
func TestDurationSurvivesPocketBaseLogWriter(t *testing.T) {
	t.Parallel()

	attr := Duration("duration", 1500*time.Millisecond)
	if attr.Key != "duration" {
		t.Fatalf("Duration().Key = %q, want %q", attr.Key, "duration")
	}
	if got := attr.Value.Kind(); got != slog.KindString {
		t.Fatalf("Duration().Value.Kind() = %v, want %v", got, slog.KindString)
	}
	if got := attr.Value.String(); got != "1.5s" {
		t.Fatalf("Duration() = %q, want %q", got, "1.5s")
	}

	if _, err := (types.JSONMap[any]{attr.Key: attr.Value.Any()}).MarshalJSON(); err != nil {
		t.Fatalf("PocketBase cannot persist the helper's value: %v", err)
	}

	if _, err := (types.JSONMap[any]{"duration": 1500 * time.Millisecond}).MarshalJSON(); err == nil {
		t.Log("a raw time.Duration now marshals; this helper may no longer be needed")
	}
}

// TestDurationRoundsToMillisecond keeps the logs readable: these attributes are
// read by people, and a raw nanosecond count is not what they replaced.
func TestDurationRoundsToMillisecond(t *testing.T) {
	t.Parallel()

	if got := Duration("in", 1234567*time.Nanosecond).Value.String(); got != "1ms" {
		t.Fatalf("Duration() = %q, want %q", got, "1ms")
	}
}
