package archiveimport

import "testing"

func TestSetMaxEntryBytes(t *testing.T) {
	original := maxEntryBytes
	t.Cleanup(func() { maxEntryBytes = original })

	for _, tc := range []struct {
		name string
		set  int64
		want int64
	}{
		// A negative value is how "no limit configured" arrives.
		{"unlimited restores the default", -1, DefaultMaxEntryBytes},
		{"a smaller cap applies", 1 << 20, 1 << 20},
		// Zero is a real cap, not a way of saying unlimited: an explicit
		// LIMIT_FILE_BYTES=0 must leave the restore preview agreeing with the
		// create hook, which refuses every non-empty file.
		{"zero is a real cap", 0, 0},
		// Nothing here can lift the documents.file field's own MaxSize.
		{"a larger cap is clamped", DefaultMaxEntryBytes * 10, DefaultMaxEntryBytes},
	} {
		SetMaxEntryBytes(tc.set)
		if maxEntryBytes != tc.want {
			t.Fatalf("%s: SetMaxEntryBytes(%d) left %d, want %d",
				tc.name, tc.set, maxEntryBytes, tc.want)
		}
	}
}

// Wiring calls this on every boot, so it must raise the cap back to the default
// as well as lower it -- otherwise the e2e harness, which boots repeatedly in one
// test binary, leaks the smallest cap any earlier boot set.
func TestSetMaxEntryBytesIsNotOneWay(t *testing.T) {
	original := maxEntryBytes
	t.Cleanup(func() { maxEntryBytes = original })

	SetMaxEntryBytes(1024)
	SetMaxEntryBytes(-1)
	if maxEntryBytes != DefaultMaxEntryBytes {
		t.Fatalf("cap stuck at %d after an unlimited boot, want %d",
			maxEntryBytes, DefaultMaxEntryBytes)
	}
}
