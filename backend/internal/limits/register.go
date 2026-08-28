package limits

import (
	"github.com/pocketbase/pocketbase/core"
)

// Register binds the limit checks.
//
// Two hooks are all the enforcement there is, because every way a document or an
// account comes into being ends in app.Save on one of these two collections:
// the SPA's collection-API upload, the paperless-ngx post_document endpoint, a
// PDF split, an Amazon-orders import, a backup restore, a paperless-ngx remote
// pull, the setup wizard, the superuser CLI, and the PocketBase admin UI.
//
// Bind this before worker.Register in appwire so an over-limit upload is refused
// before duplicates.AssignChecksumFromUpload reads the whole file to hash it.
// PocketBase sorts equal-priority handlers stably, so registration order is what
// decides that.
//
// An unlimited install binds nothing at all: no hook, no query per upload, no
// page count, no behaviour change whatsoever.
func Register(app core.App, lim Limits) {
	if !lim.Any() {
		return
	}

	app.Logger().Info("instance limits active", limitAttrs(lim)...)

	if boundsDocuments(lim) {
		app.OnRecordCreate("documents").BindFunc(func(e *core.RecordEvent) error {
			if err := applyDocumentLimits(e.App, lim, e.Record, 1); err != nil {
				if exceeded := AsExceeded(err); exceeded != nil {
					return exceeded.APIError()
				}
				return err
			}
			return e.Next()
		})

		// An update can replace the file. documents.UpdateRule lets an owner
		// patch their own row, so without this an account creates a one-page
		// document and then patches a 500-page file onto it: the stored bytes
		// change and the measurements this instance charges against do not.
		//
		// Nothing in this codebase replaces a document's file -- every
		// record.Set("file", ...) is on a freshly built record -- so in practice
		// this fires only for a client doing it deliberately.
		app.OnRecordUpdate("documents").BindFunc(func(e *core.RecordEvent) error {
			// An update that brings no file must not be able to restate what
			// this document costs. Marking the two columns Hidden stops a
			// regular account writing them, but only a regular account:
			// PocketBase's GrantSuperuserAccess is documented as allowing
			// "changing all system record fields, including those marked as
			// Hidden", so a superuser could otherwise PATCH size_bytes to 0 and
			// mint headroom -- which is precisely the thing these limits exist
			// to take out of an admin's hands.
			restoreMeasurements(e.Record)

			// A replacement adds no new document, so the count limit is not in
			// play: 0 documents, and pages and bytes are charged net of what
			// this record already contributed.
			if err := applyDocumentLimits(e.App, lim, e.Record, 0); err != nil {
				if exceeded := AsExceeded(err); exceeded != nil {
					return exceeded.APIError()
				}
				return err
			}
			return e.Next()
		})
	}

	if !lim.AdditionalUsers.IsUnlimited() {
		app.OnRecordCreate("users").BindFunc(func(e *core.RecordEvent) error {
			if err := applyUserLimit(e.App, lim, e.Record); err != nil {
				if exceeded := AsExceeded(err); exceeded != nil {
					return exceeded.APIError()
				}
				return err
			}
			return e.Next()
		})
	}
}

// boundsDocuments reports whether any limit touches a document. FileBytes and
// FilePages alone are enough: the hook also stamps size_bytes and page_count, so
// it has to run whenever anything measures a document.
func boundsDocuments(lim Limits) bool {
	return !lim.Documents.IsUnlimited() ||
		!lim.DocumentPages.IsUnlimited() ||
		!lim.StorageBytes.IsUnlimited() ||
		!lim.FileBytes.IsUnlimited() ||
		!lim.FilePages.IsUnlimited()
}

// applyDocumentLimits measures the upload, records the measurements on the
// record, and refuses it if it does not fit.
//
// The measurements are set even when nothing is exceeded, and that is the reason
// this runs for every create rather than only when a count limit is set: without
// them the sums in Measure would not include the document about to be stored.
func applyDocumentLimits(app core.App, lim Limits, record *core.Record, addsDocuments int64) error {
	files := record.GetUnsavedFiles("file")
	if len(files) == 0 {
		// No new file: a create that attaches its file elsewhere, or -- far more
		// often -- one of the many metadata updates the pipeline makes. Nothing
		// to measure and nothing to re-charge.
		return nil
	}
	file := files[0]

	sizeBytes := file.Size
	pageCount := PageCountOfUpload(app.Logger(), file)

	// What this record already contributes to the totals, so a replacement is
	// charged for the difference rather than counted twice. Zero on a create.
	priorBytes := int64(record.GetFloat("size_bytes"))
	priorPages := int64(record.GetFloat("page_count"))

	record.Set("size_bytes", sizeBytes)
	record.Set("page_count", pageCount)

	if err := lim.CheckFile(sizeBytes, pageCount); err != nil {
		return err
	}

	if !needsRoomCheck(lim) {
		return nil
	}
	usage, err := Measure(app)
	if err != nil {
		return err
	}
	// Two uploads racing can both read the same totals and both pass a check
	// only one should. Left alone deliberately: the overshoot is bounded by how
	// many uploads are in flight, the next upload sees the true total and
	// refuses, and the alternatives -- serializing every upload, or a
	// transactional counter row -- cost more than the problem.
	return lim.CheckRoom(usage, addsDocuments, pageCount-priorPages, sizeBytes-priorBytes)
}

// restoreMeasurements puts the stored size_bytes and page_count back, discarding
// whatever the request carried.
//
// applyDocumentLimits overwrites both from the file whenever an update brings
// one, so this only has to cover the file-less case -- and it runs before that,
// so an update that does bring a file still ends up with the measured values.
// Restoring rather than rejecting matches what PocketBase already does for a
// hidden field on a non-superuser write, and leaves every other edit on the
// record working.
//
// The cost is that nobody can hand-correct a measurement, superuser included.
// That is the intended trade: a plan an admin can edit is not a plan.
func restoreMeasurements(record *core.Record) {
	original := record.Original()
	record.Set("size_bytes", original.GetFloat("size_bytes"))
	record.Set("page_count", original.GetFloat("page_count"))
}

func needsRoomCheck(lim Limits) bool {
	return !lim.Documents.IsUnlimited() ||
		!lim.DocumentPages.IsUnlimited() ||
		!lim.StorageBytes.IsUnlimited()
}

// applyUserLimit refuses an account beyond the allowance.
//
// One account is free, structurally, so the setup wizard's first admin always
// passes -- with nothing in the users table the projected seat count is zero,
// whatever the limit -- and no exemption for is_app_admin is needed or wanted
// here. See CountAdditionalUsers for why exempting the flag would be a bypass.
func applyUserLimit(app core.App, lim Limits, _ *core.Record) error {
	total, err := app.CountRecords("users")
	if err != nil {
		return err
	}
	// +1 for the record about to be inserted: this hook runs before the INSERT,
	// so the table does not hold it yet.
	return lim.CheckAdditionalUsers(additionalOf(total + 1))
}

func limitAttrs(lim Limits) []any {
	attrs := make([]any, 0, 12)
	for _, entry := range []struct {
		name  string
		limit Limit
	}{
		{NameDocuments, lim.Documents},
		{NameDocumentPages, lim.DocumentPages},
		{NameStorageBytes, lim.StorageBytes},
		{NameFileBytes, lim.FileBytes},
		{NameFilePages, lim.FilePages},
		{NameAdditionalUsers, lim.AdditionalUsers},
	} {
		if entry.limit.IsUnlimited() {
			continue
		}
		attrs = append(attrs, entry.name, entry.limit.Value())
	}
	return attrs
}
