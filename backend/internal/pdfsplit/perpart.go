package pdfsplit

import "lemmary/backend/internal/models"

// DefaultMaxPartBytes is the per-part cap with no instance limit configured. It
// mirrors the documents.file field limit, which is what actually stores a part.
const DefaultMaxPartBytes = models.MaxFileBytes

// SetMaxPartBytes sets the per-part cap to the effective per-document limit.
//
// Wiring-time only, so an instance whose plan caps a single document below the
// field limit reports an over-cap part as skipped rather than having the create
// hook reject it partway through a split.
//
// A set rather than a one-way lower: the e2e harness boots a whole app
// repeatedly inside one test binary, and a lower-only setter would leak the
// smallest limit any earlier boot configured into every later one.
//
// A negative n means "use the default"; 0 is a real cap of zero, because an
// explicit LIMIT_FILE_BYTES=0 must not read as unlimited here while the create
// hook refuses every file. Clamped to DefaultMaxPartBytes, which is the field's
// own MaxSize.
func SetMaxPartBytes(n int64) {
	if n < 0 || n > DefaultMaxPartBytes {
		maxPartBytes = DefaultMaxPartBytes
		return
	}
	maxPartBytes = n
}
