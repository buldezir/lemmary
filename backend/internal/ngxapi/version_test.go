package ngxapi

import "testing"

func TestParseAcceptVersion(t *testing.T) {
	t.Parallel()

	if got := parseAcceptVersion("application/json; version=9"); got != 9 {
		t.Fatalf("parseAcceptVersion() = %d, want 9", got)
	}
	if got := parseAcceptVersion("application/json"); got != 0 {
		t.Fatalf("parseAcceptVersion() = %d, want 0", got)
	}
}
