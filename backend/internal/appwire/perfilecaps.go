package appwire

import (
	"lemmary/backend/internal/amazonimport"
	"lemmary/backend/internal/archiveimport"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/pdfsplit"
)

// applyPerFileCaps points the bulk paths' per-entry size caps at the effective
// per-document limit.
//
// Those caps exist to mirror the documents.file field limit, so an entry that
// could never be stored is reported as skipped instead of failing the whole run.
// A per-file limit is the same statement, only smaller, so feeding it through
// the same caps gets that behaviour for free -- and keeps the import previews
// honest: without it a preview would mark an entry importable at a size the
// create hook then refuses.
//
// Called unconditionally, including with no limit set, so each cap lands back on
// its default rather than keeping whatever an earlier call left. That matters in
// the e2e harness, which boots a whole app repeatedly inside one test binary.
//
// It lives here rather than in the limits package so that package stays a leaf:
// archiveimport and amazonimport are free to import limits, and this direction
// would close the cycle.
func applyPerFileCaps(lim limits.Limits) {
	// Unlimited resolves to 0, which every setter reads as "use your default".
	// Nothing here can raise a cap above the field's own MaxSize anyway.
	var effective int64
	if !lim.FileBytes.IsUnlimited() {
		effective = lim.FileBytes.Value()
	}
	amazonimport.SetMaxEntryBytes(effective)
	archiveimport.SetMaxEntryBytes(effective)
	pdfsplit.SetMaxPartBytes(effective)
}
