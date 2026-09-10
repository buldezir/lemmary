package appwire

import (
	"testing"

	"lemmary/backend/internal/limits"
)

// The zero row is the one that matters: LIMIT_ADDITIONAL_USERS=0 is a real
// allowance -- this account and no others -- so it must reach the gauge, while
// an unset one must not. Collapsing the two would either publish a cap nobody
// set or hide one somebody paid for.
func TestSetLimits(t *testing.T) {
	t.Parallel()

	got := setLimits(map[string]limits.Limit{
		limits.NameDocuments:       limits.Of(1000),
		limits.NameAdditionalUsers: limits.Of(0),
		limits.NameDocumentPages:   limits.Unlimited(),
		limits.NameFilePages:       {},
	})

	want := map[string]int64{
		limits.NameDocuments:       1000,
		limits.NameAdditionalUsers: 0,
	}
	if len(got) != len(want) {
		t.Fatalf("setLimits returned %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("setLimits[%q] = %d, want %d", name, got[name], value)
		}
	}
}
