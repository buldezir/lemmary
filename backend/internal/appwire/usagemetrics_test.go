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

// Per-file ceilings are a property of one upload, not a stock the instance
// holds. Publishing them on lemmary.limit next to documents would make a
// dashboard dividing usage by limit treat "200 pages per file" as a library
// cap of 200 pages.
func TestInstanceLimitsOmitPerFileCaps(t *testing.T) {
	t.Parallel()

	counts, bytes := instanceLimits(limits.Limits{
		Documents:       limits.Of(1000),
		DocumentPages:   limits.Of(10000),
		AdditionalUsers: limits.Of(0),
		StorageBytes:    limits.Of(5368709120),
		FilePages:       limits.Of(200),
		FileBytes:       limits.Of(10485760),
	})

	for _, leaked := range []string{limits.NameFilePages, limits.NameOCRPages} {
		if _, ok := counts[leaked]; ok {
			t.Errorf("%s leaked onto the instance count-limit series: %v", leaked, counts)
		}
	}
	if _, ok := bytes[limits.NameFileBytes]; ok {
		t.Errorf("file_bytes leaked onto the instance byte-limit series: %v", bytes)
	}
	if counts[limits.NameDocuments] != 1000 || counts[limits.NameAdditionalUsers] != 0 {
		t.Errorf("instance count limits = %v, want documents=1000 additional_users=0", counts)
	}
	if bytes[limits.NameStorageBytes] != 5368709120 {
		t.Errorf("instance byte limits = %v, want storage_bytes=5368709120", bytes)
	}
}
