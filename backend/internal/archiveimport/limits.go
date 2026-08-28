package archiveimport

// DefaultMaxEntryBytes is the per-entry cap with no instance limit configured.
// It mirrors the documents.file field limit, which is what actually stores the
// restored file.
const DefaultMaxEntryBytes int64 = 20 << 20

// SetMaxEntryBytes sets the per-entry cap to the effective per-document limit.
//
// Wiring-time only, called before any request is served, so an instance whose
// plan caps a single document below the field limit reports an over-cap entry as
// oversized in the restore preview -- the behaviour this package already has for
// the field limit -- rather than accepting it and having the create hook reject
// it partway through a restore. Without this the preview would lie: it would
// mark a 15 MB entry importable and the restore would then refuse it.
//
// A set rather than a one-way lower: the value has to be able to go back up. The
// e2e harness boots a whole app repeatedly inside one test binary, and a
// lower-only setter would leak the smallest limit any earlier boot configured
// into every later one.
//
// Clamped to DefaultMaxEntryBytes, because nothing here can lift the field's own
// MaxSize -- PocketBase validates that during the save.
func SetMaxEntryBytes(n int64) {
	if n <= 0 || n > DefaultMaxEntryBytes {
		maxEntryBytes = DefaultMaxEntryBytes
		return
	}
	maxEntryBytes = n
}
