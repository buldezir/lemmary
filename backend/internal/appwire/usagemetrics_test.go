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

// A per-file ceiling is published beside the instance-wide ones, since the
// orchestrator that set it checks the container honours it. The built-in OCR
// page ceiling is not a plan and never is.
func TestInstanceLimitsCarryPerFileCaps(t *testing.T) {
	t.Parallel()

	counts, bytes := instanceLimits(limits.Limits{
		Documents:       limits.Of(1000),
		DocumentPages:   limits.Of(10000),
		AdditionalUsers: limits.Of(0),
		StorageBytes:    limits.Of(5368709120),
		FilePages:       limits.Of(200),
		FileBytes:       limits.Of(10485760),
	})

	if _, ok := counts[limits.NameOCRPages]; ok {
		t.Errorf("ocr_pages leaked onto the count-limit series: %v", counts)
	}
	if counts[limits.NameDocuments] != 1000 || counts[limits.NameAdditionalUsers] != 0 ||
		counts[limits.NameFilePages] != 200 {
		t.Errorf("count limits = %v, want documents=1000 additional_users=0 file_pages=200", counts)
	}
	if bytes[limits.NameStorageBytes] != 5368709120 || bytes[limits.NameFileBytes] != 10485760 {
		t.Errorf("byte limits = %v, want storage_bytes=5368709120 file_bytes=10485760", bytes)
	}
}
