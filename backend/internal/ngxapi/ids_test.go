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

func TestOwnerScopeBindsAuthID(t *testing.T) {
	t.Parallel()

	filter, params := ownerScope("user-a")
	if filter != "user = {:userId}" {
		t.Fatalf("filter=%q", filter)
	}
	if params["userId"] != "user-a" {
		t.Fatalf("params=%v", params)
	}

	_, other := ownerScope("user-b")
	if other["userId"] != "user-b" {
		t.Fatalf("second scope params=%v", other)
	}

	emptyFilter, emptyParams := ownerScope("")
	if emptyFilter != "" || emptyParams != nil {
		t.Fatalf("empty owner should be unscoped, filter=%q params=%v", emptyFilter, emptyParams)
	}
}
