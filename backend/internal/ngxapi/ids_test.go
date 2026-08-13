package ngxapi

import "testing"

func TestToNgxIDStable(t *testing.T) {
	t.Parallel()

	id := "abc123xyz456789"
	first := toNgxID(id)
	second := toNgxID(id)
	if first != second {
		t.Fatalf("toNgxID not stable: %d vs %d", first, second)
	}
	if first <= 0 {
		t.Fatalf("toNgxID must be positive, got %d", first)
	}
}
