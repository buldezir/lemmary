package logfmt

import (
	"log/slog"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tools/types"
)

// The second half asserts the raw time.Duration failure directly, so the helper
// can be retired on evidence if Go or PocketBase ever marshals one.
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

func TestDurationRoundsToMillisecond(t *testing.T) {
	t.Parallel()

	if got := Duration("in", 1234567*time.Nanosecond).Value.String(); got != "1ms" {
		t.Fatalf("Duration() = %q, want %q", got, "1ms")
	}
}
