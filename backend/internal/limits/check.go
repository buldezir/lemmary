package limits

import (
	"fmt"

	"github.com/pocketbase/pocketbase/tools/router"
)

// Names identify a limit in an error payload and in the usage API, so a client
// can say which allowance ran out without parsing an English sentence.
const (
	NameDocuments       = "documents"
	NameDocumentPages   = "document_pages"
	NameStorageBytes    = "storage_bytes"
	NameFileBytes       = "file_bytes"
	NameFilePages       = "file_pages"
	NameAdditionalUsers = "additional_users"

	// NameOCRPages is not one of the six env limits. It identifies the built-in
	// ceiling below, which every install carries.
	NameOCRPages = "ocr_pages"
)

// MaxOCRPages is the most pages a document may hold, on any install.
//
// Not an allowance a plan sells -- a property of what this can extract. The OCR
// providers here return a document's whole text in one string, and that string
// has to fit models.MaxOCRTextRunes. Nothing else bounds it: Mistral is the only
// provider that documents a page limit at all (1000 pages, 50 MB), and Google
// Vision simply loops every page of the file five at a time and concatenates.
// So the page count is the one measurement taken before any provider is called
// that says whether the result could be stored.
//
// 1000 because that is Mistral's number, and because it is comfortably inside
// the character ceiling: a full A4 page of 6pt text is around 15,000 characters,
// so even 20,000 a page over 1000 pages stays under 20,971,520.
//
// LIMIT_FILE_PAGES can lower this and cannot raise it, the same way
// LIMIT_FILE_BYTES relates to the 20 MB documents.file MaxSize.
const MaxOCRPages int64 = 1000

// ErrExceeded is a limit refusing something. It carries the numbers so a caller
// can render "3 of 3 used" without measuring again.
type ErrExceeded struct {
	// Name is one of the Name* constants.
	Name string
	// Allowed is the limit.
	Allowed int64
	// Used is what was already in use when the check ran. For a per-file limit
	// this is the value the file itself presented.
	Used int64
	// Message is the human-readable explanation.
	Message string
}

func (e *ErrExceeded) Error() string { return e.Message }

// Code implements router.SafeErrorItem, identifying which limit was hit.
//
// This is what makes the limit name survive the trip to the client. PocketBase
// replaces any value in an ApiError's data map that does not implement
// SafeErrorItem with a generic {"code": "validation_invalid_value"} -- which is
// why the duplicate rejection next door has to parse its id back out of the
// message text. Implementing the interface means a client can switch on the code
// instead.
func (e *ErrExceeded) Code() string { return "limit_" + e.Name }

// Params implements router.SafeErrorParamsResolver, carrying the numbers so a
// client can render "3 of 3 used" without measuring anything itself.
func (e *ErrExceeded) Params() map[string]any {
	return map[string]any{
		"limit":   e.Name,
		"allowed": e.Allowed,
		"used":    e.Used,
	}
}

// APIError renders the rejection for an HTTP client.
//
// A 400, like the only other business rejection on this collection (duplicates).
// Not 413 or 507: PocketBase's own file-size rejection on documents is already a
// 400, so a different status for the same class of problem would be the odd one
// out, and the paperless-ngx clients that also reach these paths understand no
// quota status either. The 423 handling in the SPA is reserved for a private
// edition.
//
// The error is put under a "limit" key so the whole payload reads as
// {"limit": {"code": "limit_documents", "params": {...}}}.
func (e *ErrExceeded) APIError() *router.ApiError {
	return router.NewBadRequestError(e.Message, map[string]any{"limit": e})
}

// AsExceeded returns the *ErrExceeded an error carries, if any. Mirrors the
// shape duplicates uses so an ingest path can test for one type.
func AsExceeded(err error) *ErrExceeded {
	if err == nil {
		return nil
	}
	if exceeded, ok := err.(*ErrExceeded); ok {
		return exceeded
	}
	return nil
}

// CheckOCRPages refuses a file with more pages than its text could be stored
// from.
//
// A method on nothing -- it is not one of the env limits and does not vary by
// install -- but it returns the same *ErrExceeded so a client reads it exactly
// like the allowances next to it, and so it renders as the same 400.
//
// Checked at upload rather than in the OCR step because the point is to spend
// nothing on a file whose text has nowhere to go: the provider call is the
// expensive part, and by the time it returns the money is gone.
func CheckOCRPages(pageCount int64) error {
	if pageCount <= MaxOCRPages {
		return nil
	}
	return &ErrExceeded{
		Name:    NameOCRPages,
		Allowed: MaxOCRPages,
		Used:    pageCount,
		Message: fmt.Sprintf(
			"This file has %d pages. Text can be extracted from at most %d.",
			pageCount, MaxOCRPages),
	}
}

// CheckFile applies the two per-upload limits to one file's own measurements.
func (l Limits) CheckFile(sizeBytes, pageCount int64) error {
	if l.FileBytes.Exceeded(sizeBytes) {
		return &ErrExceeded{
			Name:    NameFileBytes,
			Allowed: l.FileBytes.Value(),
			Used:    sizeBytes,
			Message: fmt.Sprintf(
				"This file is %s, over the %s limit for a single document.",
				formatBytes(sizeBytes), formatBytes(l.FileBytes.Value())),
		}
	}
	if l.FilePages.Exceeded(pageCount) {
		return &ErrExceeded{
			Name:    NameFilePages,
			Allowed: l.FilePages.Value(),
			Used:    pageCount,
			Message: fmt.Sprintf(
				"This file has %d pages, over the %d-page limit for a single document.",
				pageCount, l.FilePages.Value()),
		}
	}
	return nil
}

// CheckRoom applies the three instance-wide document limits to what adding
// documents worth of pages and bytes would bring the totals to.
//
// documents, pages and bytes are the additions, not the new totals.
func (l Limits) CheckRoom(usage Usage, documents, pages, bytes int64) error {
	if l.Documents.Exceeded(usage.Documents + documents) {
		return &ErrExceeded{
			Name:    NameDocuments,
			Allowed: l.Documents.Value(),
			Used:    usage.Documents,
			Message: documentCountMessage(l.Documents.Value(), usage.Documents, documents),
		}
	}
	if l.DocumentPages.Exceeded(usage.DocumentPages + pages) {
		return &ErrExceeded{
			Name:    NameDocumentPages,
			Allowed: l.DocumentPages.Value(),
			Used:    usage.DocumentPages,
			Message: fmt.Sprintf(
				"This instance holds %d of %d pages, and this would add %d.",
				usage.DocumentPages, l.DocumentPages.Value(), pages),
		}
	}
	if l.StorageBytes.Exceeded(usage.StorageBytes + bytes) {
		return &ErrExceeded{
			Name:    NameStorageBytes,
			Allowed: l.StorageBytes.Value(),
			Used:    usage.StorageBytes,
			Message: fmt.Sprintf(
				"This instance uses %s of its %s of storage, and this would add %s.",
				formatBytes(usage.StorageBytes), formatBytes(l.StorageBytes.Value()),
				formatBytes(bytes)),
		}
	}
	return nil
}

func documentCountMessage(allowed, used, adding int64) string {
	if adding == 1 {
		return fmt.Sprintf(
			"This instance holds %d of %d documents, so there is no room for another.",
			used, allowed)
	}
	return fmt.Sprintf(
		"This instance holds %d of %d documents, so there is no room for %d more.",
		used, allowed, adding)
}

// CheckAdditionalUsers applies the account limit to a projected seat count --
// what CountAdditionalUsers would report once the account in question exists.
//
// It takes the result rather than the current count so the caller owns the "one
// account is free" arithmetic in one place, instead of this having to know
// whether the caller already counted the new record.
func (l Limits) CheckAdditionalUsers(projected int64) error {
	if !l.AdditionalUsers.Exceeded(projected) {
		return nil
	}
	allowed := l.AdditionalUsers.Value()
	message := fmt.Sprintf(
		"This instance allows %d accounts beyond the admin account and already has that many.",
		allowed)
	if allowed == 0 {
		message = "This instance does not allow accounts beyond the admin account."
	}
	return &ErrExceeded{
		Name:    NameAdditionalUsers,
		Allowed: allowed,
		Used:    projected - 1,
		Message: message,
	}
}

// formatBytes renders a byte count the way a person reads a file size. Binary
// units, because that is what the existing size caps in this codebase are
// expressed in.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			if value < 10 {
				return fmt.Sprintf("%.1f %s", value, suffix)
			}
			return fmt.Sprintf("%.0f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.0f PB", value/unit)
}
