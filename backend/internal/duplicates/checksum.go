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

func SHA256Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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

// The earliest document owned by userID with the same checksum.
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

// Nil when saveErr is not a checksum uniqueness conflict.
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

// Reads {"duplicate_of": id} out of a PocketBase ApiError's raw data, as the
// documents create hook returns it. Empty when err is not such an ApiError.
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

func ErrDuplicateFromAPIError(err error) *ErrDuplicate {
	id := DuplicateOfFromError(err)
	if id == "" {
		return nil
	}
	return &ErrDuplicate{ExistingID: id}
}

// Folds every shape a duplicate rejection arrives in into *ErrDuplicate, so an
// ingest path tests one type: the create hook rejecting outright, the same
// rejection wrapped in an ApiError, and the unique index firing on a race.
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

// Callers must still handle unique-constraint failures from Save via
// ErrDuplicateFromSaveConflict, or concurrent uploads both succeed.
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
