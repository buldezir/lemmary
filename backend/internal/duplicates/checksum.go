package duplicates

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// ErrDuplicate is returned when an exact checksum match already exists for the user.
type ErrDuplicate struct {
	ExistingID    string
	ExistingTitle string
}

func (e *ErrDuplicate) Error() string {
	title := strings.TrimSpace(e.ExistingTitle)
	if title != "" {
		return fmt.Sprintf("document already exists (duplicate of %s: %s)", e.ExistingID, title)
	}
	return fmt.Sprintf("document already exists (duplicate of %s)", e.ExistingID)
}

// SHA256Reader hashes all bytes from r.
func SHA256Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256File hashes a PocketBase unsaved filesystem.File.
func SHA256File(file *filesystem.File) (string, error) {
	if file == nil || file.Reader == nil {
		return "", fmt.Errorf("missing file reader")
	}
	r, err := file.Reader.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	return SHA256Reader(r)
}

// FindByChecksum returns the earliest document owned by userID with the same checksum.
func FindByChecksum(app core.App, userID, checksum, excludeID string) (*core.Record, error) {
	checksum = strings.TrimSpace(checksum)
	if userID == "" || checksum == "" {
		return nil, nil
	}
	filter := "user = {:user} && checksum = {:checksum}"
	params := map[string]any{"user": userID, "checksum": checksum}
	if excludeID != "" {
		filter += " && id != {:exclude}"
		params["exclude"] = excludeID
	}
	records, err := app.FindRecordsByFilter("documents", filter, "created", 1, 0, params)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// IsChecksumUniqueViolation reports whether err is a unique (user, checksum) constraint failure.
func IsChecksumUniqueViolation(err error) bool {
	for err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "unique") &&
			(strings.Contains(msg, "checksum") || strings.Contains(msg, "idx_documents_user_checksum")) {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// ErrDuplicateFromSaveConflict maps a unique-checksum save failure to ErrDuplicate.
// Returns nil when saveErr is not a checksum uniqueness conflict.
func ErrDuplicateFromSaveConflict(app core.App, record *core.Record, saveErr error) *ErrDuplicate {
	if record == nil || !IsChecksumUniqueViolation(saveErr) {
		return nil
	}
	existing, err := FindByChecksum(app, record.GetString("user"), record.GetString("checksum"), record.Id)
	if err != nil || existing == nil {
		return &ErrDuplicate{}
	}
	return &ErrDuplicate{
		ExistingID:    existing.Id,
		ExistingTitle: existing.GetString("title"),
	}
}

// DuplicateOfFromError extracts an ExistingID from a PocketBase ApiError that
// carries {"duplicate_of": id} in its raw data (as returned by the documents create hook).
// Returns "" when err is not such an ApiError.
func DuplicateOfFromError(err error) string {
	type rawDataCarrier interface {
		RawData() any
	}
	var carrier rawDataCarrier
	if !errors.As(err, &carrier) {
		return ""
	}
	data, ok := carrier.RawData().(map[string]any)
	if !ok {
		return ""
	}
	id, _ := data["duplicate_of"].(string)
	return strings.TrimSpace(id)
}

// ErrDuplicateFromAPIError maps a create-hook ApiError with duplicate_of into ErrDuplicate.
func ErrDuplicateFromAPIError(err error) *ErrDuplicate {
	id := DuplicateOfFromError(err)
	if id == "" {
		return nil
	}
	return &ErrDuplicate{ExistingID: id}
}

// NormalizeSaveError folds every shape a duplicate rejection can arrive in into
// *ErrDuplicate, so an ingest path only has to test for that one type. Any other
// error is returned unchanged, and nil stays nil.
//
// The three shapes are the create hook rejecting the upload outright, the same
// rejection arriving wrapped in an ApiError, and the database's unique
// (user, checksum) index firing when two saves race.
func NormalizeSaveError(app core.App, record *core.Record, saveErr error) error {
	if saveErr == nil {
		return nil
	}
	var dup *ErrDuplicate
	if errors.As(saveErr, &dup) {
		return dup
	}
	if dup := ErrDuplicateFromAPIError(saveErr); dup != nil {
		return dup
	}
	if dup := ErrDuplicateFromSaveConflict(app, record, saveErr); dup != nil {
		return dup
	}
	return saveErr
}

// AssignChecksumFromUpload hashes the unsaved upload, sets checksum, and rejects duplicates.
// Callers should still handle unique-constraint failures from Save via ErrDuplicateFromSaveConflict
// so concurrent uploads cannot both succeed.
func AssignChecksumFromUpload(app core.App, record *core.Record) error {
	files := record.GetUnsavedFiles("file")
	if len(files) == 0 {
		return nil
	}
	checksum, err := SHA256File(files[0])
	if err != nil {
		return fmt.Errorf("hash uploaded file: %w", err)
	}
	record.Set("checksum", checksum)

	userID := record.GetString("user")
	existing, err := FindByChecksum(app, userID, checksum, record.Id)
	if err != nil {
		return err
	}
	if existing != nil {
		return &ErrDuplicate{
			ExistingID:    existing.Id,
			ExistingTitle: existing.GetString("title"),
		}
	}
	return nil
}

// HashDocumentFile hashes a persisted document file from storage.
func HashDocumentFile(app core.App, document *core.Record) (string, error) {
	fileName := document.GetString("file")
	if fileName == "" {
		return "", fmt.Errorf("document has no file")
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		return "", err
	}
	defer fsys.Close()

	reader, err := fsys.GetReader(document.BaseFilesPath() + "/" + fileName)
	if err != nil {
		return "", fmt.Errorf("open document file: %w", err)
	}
	defer reader.Close()
	return SHA256Reader(reader)
}
