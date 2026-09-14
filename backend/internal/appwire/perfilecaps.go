package appwire

import (
	"lemmary/backend/internal/archiveimport"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/pdfsplit"
	"lemmary/backend/internal/zipimport"
)

// applyPerFileCaps points the bulk paths' per-entry size caps at the effective
// per-document limit, so a preview cannot call an entry importable at a size
// the create hook then refuses.
//
// Called unconditionally, so with no limit set each cap lands back on its
// default rather than keeping what an earlier call left; the e2e harness boots
// a whole app repeatedly in one test binary. It lives here rather than in
// limits so that package stays a leaf that archiveimport and zipimport can
// import.
func applyPerFileCaps(lim limits.Limits) {
	// Unlimited is -1, which every setter reads as "use your default". Not 0:
	// an explicit LIMIT_FILE_BYTES=0 is a real cap.
	effective := int64(-1)
	if !lim.FileBytes.IsUnlimited() {
		effective = lim.FileBytes.Value()
	}
	zipimport.SetMaxEntryBytes(effective)
	archiveimport.SetMaxEntryBytes(effective)
	pdfsplit.SetMaxPartBytes(effective)
}
