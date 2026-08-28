package pdfsplit

import "testing"

func TestSetMaxPartBytes(t *testing.T) {
	original := maxPartBytes
	t.Cleanup(func() { maxPartBytes = original })

	for _, tc := range []struct {
		name string
		set  int64
		want int64
	}{
		// A negative value is how "no limit configured" arrives.
		{"unlimited restores the default", -1, DefaultMaxPartBytes},
		{"a smaller cap applies", 1 << 20, 1 << 20},
		// Zero is a real cap, not a way of saying unlimited: an explicit
		// LIMIT_FILE_BYTES=0 must leave the preview agreeing with the create
		// hook, which refuses every non-empty file.
		{"zero is a real cap", 0, 0},
		// Nothing here can lift the documents.file field's own MaxSize.
		{"a larger cap is clamped", DefaultMaxPartBytes * 10, DefaultMaxPartBytes},
		{"exactly the default", DefaultMaxPartBytes, DefaultMaxPartBytes},
	} {
		SetMaxPartBytes(tc.set)
		if maxPartBytes != tc.want {
			t.Fatalf("%s: SetMaxPartBytes(%d) left %d, want %d",
				tc.name, tc.set, maxPartBytes, tc.want)
		}
	}
}

// Wiring calls this on every boot, including boots that configure nothing, so it
// has to be able to raise the cap back to the default rather than only lower it.
// Without that the e2e harness -- which boots a whole app repeatedly in one test
// binary -- would leak the smallest cap any earlier boot set into every later one.
func TestSetMaxPartBytesIsNotOneWay(t *testing.T) {
	original := maxPartBytes
	t.Cleanup(func() { maxPartBytes = original })

	SetMaxPartBytes(1024)
	SetMaxPartBytes(-1)
	if maxPartBytes != DefaultMaxPartBytes {
		t.Fatalf("cap stuck at %d after an unlimited boot, want %d",
			maxPartBytes, DefaultMaxPartBytes)
	}
}
