package archiveimport

import "lemmary/backend/internal/models"

// DefaultMaxEntryBytes is the per-entry cap with no instance limit configured.
// It mirrors the documents.file field limit, which is what actually stores the
// restored file.
const DefaultMaxEntryBytes = models.MaxFileBytes

// SetMaxEntryBytes sets the per-entry cap to the effective per-document limit.
//
// Wiring-time only, called before any request is served, so an instance whose
// plan caps a single document below the field limit reports an over-cap entry
// as oversized in the preview rather than accepting it and having the create
// hook reject it partway through. Without this the preview would lie.
//
// A set rather than a one-way lower: the e2e harness boots a whole app
// repeatedly inside one test binary, and a lower-only setter would leak the
// smallest limit any earlier boot configured into every later one.
//
// A negative n means "use the default"; 0 is a real cap of zero, because an
// explicit LIMIT_FILE_BYTES=0 must not read as unlimited here while the create
// hook refuses every file. Clamped to DefaultMaxEntryBytes, which is the field's
// own MaxSize.
func SetMaxEntryBytes(n int64) {
	if n < 0 || n > DefaultMaxEntryBytes {
		maxEntryBytes = DefaultMaxEntryBytes
		return
	}
	maxEntryBytes = n
}
