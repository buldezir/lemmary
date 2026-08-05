package mail

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"paperless-go/backend/internal/models"
)

var allowedImportExt = map[string]struct{}{
	".pdf":  {},
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".webp": {},
	".txt":  {},
}

// IsAllowedImportFilename reports whether the attachment extension is supported by documents.file.
func IsAllowedImportFilename(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := allowedImportExt[ext]
	return ok
}

// ImportAttachment creates a pending document from attachment bytes.
func ImportAttachment(app core.App, userID, filename string, data []byte) (*core.Record, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty attachment")
	}
	if !IsAllowedImportFilename(filename) {
		return nil, fmt.Errorf("unsupported attachment type: %s", filename)
	}

	file, err := filesystem.NewFileFromBytes(data, filename)
	if err != nil {
		return nil, fmt.Errorf("file from bytes: %w", err)
	}

	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("file", file)
	record.Set("processing_status", models.DocStatusPending)
	if title := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)); title != "" {
		record.Set("title", title)
	}

	if err := app.Save(record); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}
	return record, nil
}

// AlreadyImported returns true if this attachment was imported before.
func AlreadyImported(app core.App, accountID, messageID, attachmentID string) (bool, error) {
	records, err := app.FindRecordsByFilter(
		"mail_imports",
		"account = {:account} && gmail_message_id = {:mid} && attachment_id = {:aid}",
		"",
		1,
		0,
		map[string]any{
			"account": accountID,
			"mid":     messageID,
			"aid":     attachmentID,
		},
	)
	if err != nil {
		return false, err
	}
	return len(records) > 0, nil
}

// RecordImport stores a successful import dedup row.
func RecordImport(app core.App, userID, accountID, messageID, attachmentID, filename, documentID string) error {
	collection, err := app.FindCollectionByNameOrId("mail_imports")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("account", accountID)
	record.Set("gmail_message_id", messageID)
	record.Set("attachment_id", attachmentID)
	record.Set("filename", filename)
	record.Set("document", documentID)
	return app.Save(record)
}
