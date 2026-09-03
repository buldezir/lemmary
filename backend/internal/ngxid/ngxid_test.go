package ngxid

import "testing"

func TestHashIsStableAndInRange(t *testing.T) {
	t.Parallel()
	const pbID = "abc123def456ghi"
	first, second := Hash(pbID), Hash(pbID)
	if first != second {
		t.Fatalf("Hash is not stable: %d vs %d", first, second)
	}
	if first < 1 || first > Max {
		t.Fatalf("Hash returned %d, want 1..%d", first, Max)
	}
}

// TestHashNeverReturnsZero: 0 is what the column holds for a row no hook
// stamped, and findRecordByNgxID refuses it. Seeding a real record with 0 would
// make that record unreachable.
func TestHashNeverReturnsZero(t *testing.T) {
	t.Parallel()
	if got := Hash(""); got != 0 {
		t.Fatalf("Hash(\"\") = %d, want 0 -- an empty id is not a record", got)
	}
	// The only input that hashes to 0 is mapped to 1 instead; assert the guard
	// rather than searching for that input.
	if got := Free(0, func(int) bool { return false }); got != 1 {
		t.Fatalf("Free(0) = %d, want 1", got)
	}
}

func TestFreeWalksPastTakenIDs(t *testing.T) {
	t.Parallel()
	taken := map[int]bool{100: true, 101: true, 102: true}
	if got := Free(100, func(id int) bool { return taken[id] }); got != 103 {
		t.Fatalf("Free = %d, want 103", got)
	}
}

// TestFreeWrapsAtMax: ids are handed out inside a signed 32-bit range because
// that is what a paperless client decodes them as, so the walk cannot simply
// run off the end.
func TestFreeWrapsAtMax(t *testing.T) {
	t.Parallel()
	taken := map[int]bool{Max: true}
	if got := Free(Max, func(id int) bool { return taken[id] }); got != 1 {
		t.Fatalf("Free at the ceiling = %d, want it to wrap to 1", got)
	}
}

// TestFreeGivesUp rather than spinning: a run of maxProbes consecutive taken ids
// cannot arise from hashing, so it means the table is wrong and the write
// should fail saying so.
func TestFreeGivesUp(t *testing.T) {
	t.Parallel()
	if got := Free(1, func(int) bool { return true }); got != 0 {
		t.Fatalf("Free over a fully taken range = %d, want 0", got)
	}
}
