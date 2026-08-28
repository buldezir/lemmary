package pdfsplit

// DefaultMaxPartBytes is the per-part cap with no instance limit configured. It
// mirrors the documents.file field limit, which is what actually stores a part.
const DefaultMaxPartBytes int64 = 20 << 20

// SetMaxPartBytes sets the per-part cap to the effective per-document limit.
//
// Wiring-time only, called before any request is served, so an instance whose
// plan caps a single document below the field limit reports an over-cap part as
// skipped -- the behaviour this package already has for the field limit --
// rather than having the create hook reject it partway through a split.
//
// A set rather than a one-way lower: the value has to be able to go back up. The
// e2e harness boots a whole app repeatedly inside one test binary, and a
// lower-only setter would leak the smallest limit any earlier boot configured
// into every later one.
//
// Clamped to DefaultMaxPartBytes, because nothing here can lift the field's own
// MaxSize -- PocketBase validates that during the save.
func SetMaxPartBytes(n int64) {
	if n <= 0 || n > DefaultMaxPartBytes {
		maxPartBytes = DefaultMaxPartBytes
		return
	}
	maxPartBytes = n
}
