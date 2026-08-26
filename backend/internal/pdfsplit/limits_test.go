package pdfsplit

import (
	"testing"
)

func TestMaxPDFBytesFromEnv(t *testing.T) {
	const def = int64(DefaultMaxUploadMB) << 20

	for _, tc := range []struct {
		name string
		raw  string
		want int64
	}{
		{"unset falls back", "", def},
		{"blank falls back", "   ", def},
		{"a plain value is megabytes", "25", 25 << 20},
		{"whitespace is trimmed", " 7 ", 7 << 20},
		// A typo in an orchestrator's environment must not take an instance
		// down, and must not silently become a zero-byte cap either.
		{"a typo falls back", "1OO", def},
		{"zero falls back", "0", def},
		{"negative falls back", "-5", def},
		{"a fractional value falls back", "1.5", def},
		// mb << 20 would overflow into a negative cap, which io.LimitReader
		// reads as "allow nothing".
		{"an absurd value falls back", "9223372036854775807", def},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("UPLOAD_MAX_MB", tc.raw)

			if got := maxPDFBytesFromEnv(); got != tc.want {
				t.Fatalf("UPLOAD_MAX_MB=%q gave %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// The cap is a var read at start, not a constant, so a positive value has to
// actually be reachable from the environment.
func TestMaxPDFBytesIsPositive(t *testing.T) {
	t.Parallel()

	if MaxPDFBytes <= 0 {
		t.Fatalf("MaxPDFBytes=%d", MaxPDFBytes)
	}
}
