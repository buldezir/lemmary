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

	// NameOCRPages is not one of the six env limits: it identifies the built-in
	// ceiling below, which every install carries.
	NameOCRPages = "ocr_pages"
)

// MaxOCRPages is a property of what this can extract, not an allowance a plan
// sells: the OCR providers return a document's whole text in one string, which
// has to fit models.MaxOCRTextRunes, and nothing else bounds it. So the page
// count is the one measurement taken before any provider is called that says
// whether the result could be stored.
//
// A property of what this can extract, not an allowance a plan sells: OCR
// providers return the whole text in one string that must fit
// models.MaxOCRTextRunes, and the page count is the one measurement taken before
// any provider is called. 1000 is Mistral's documented limit and is comfortably
// inside the character ceiling: even 20,000 characters a page over 1000 pages
// stays under 20,971,520. LIMIT_FILE_PAGES can lower this and cannot raise it.
const MaxOCRPages int64 = 1000

// ErrExceeded carries the numbers, so a caller can render "3 of 3 used" without
// measuring again.
type ErrExceeded struct {
	Name    string
	Allowed int64
	// Used is what was in use when the check ran; for a per-file limit, the value
	// the file itself presented.
	Used    int64
	Message string
}

func (e *ErrExceeded) Error() string { return e.Message }

// Code implements router.SafeErrorItem, which is what makes the limit name
// survive the trip to the client: PocketBase replaces any value in an ApiError's
// data map that does not implement it with a generic validation_invalid_value,
// which is why the duplicate rejection next door parses its id out of the text.
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

// APIError answers 400, like the only other business rejection on this
// collection: PocketBase's own file-size rejection is a 400 too, and the
// paperless-ngx clients understand no quota status. Not 423 either, which is
// what the vault's unlock gate answers and which makes the SPA reload.
//
// The error goes under a "limit" key, so the payload reads as
// {"limit": {"code": "limit_documents", "params": {...}}}.
func (e *ErrExceeded) APIError() *router.ApiError {
	return router.NewBadRequestError(e.Message, map[string]any{"limit": e})
}

// AsExceeded mirrors the shape duplicates uses, so an ingest path can test for
// one type.
func AsExceeded(err error) *ErrExceeded {
	if err == nil {
		return nil
	}
	if exceeded, ok := err.(*ErrExceeded); ok {
		return exceeded
	}
	return nil
}

// CheckOCRPages returns the same *ErrExceeded as the env allowances, so a client
// reads it the same way, though it varies by no install. Checked at upload
// rather than in the OCR step so nothing is spent on a file whose text has
// nowhere to go: by the time the provider call returns the money is gone.
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

// CheckRoom takes documents, pages and bytes as the additions, not the new
// totals.
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

// CheckAdditionalUsers takes the projected seat count rather than the current
// one, so the caller owns the "one account is free" arithmetic in one place.
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

// formatBytes uses binary units, matching the existing size caps here.
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
