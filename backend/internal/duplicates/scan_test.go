package duplicates

import (
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/testpb"
	// Blank import on purpose: the shared schema template only includes what
	// this package registered, and the duplicates package never imports it.
	_ "lemmary/backend/migrations"
)

const scanTestContent = "identical file bytes"

func saveScanTestUser(t *testing.T, app core.App) string {
	t.Helper()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(users)
	user.Set("email", "scan@example.test")
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return user.Id
}

func saveScanTestDocument(t *testing.T, app core.App, userID, checksum, created string) *core.Record {
	t.Helper()
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	file, err := filesystem.NewFileFromBytes([]byte(scanTestContent), "scan.txt")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	createdAt, err := types.ParseDateTime(created)
	if err != nil {
		t.Fatalf("parse created: %v", err)
	}
	document := core.NewRecord(documents)
	document.Set("user", userID)
	document.Set("file", file)
	document.Set("checksum", checksum)
	document.SetRaw("created", createdAt)
	document.Set("ocr_text", "Invoice 2026-001 from Acme GmbH for consulting services rendered in August")
	if err := app.Save(document); err != nil {
		t.Fatalf("save document: %v", err)
	}
	return document
}

func scanTestChecksum(t *testing.T) string {
	t.Helper()
	checksum, err := SHA256Reader(strings.NewReader(scanTestContent))
	if err != nil {
		t.Fatal(err)
	}
	return checksum
}

func reloadScanTestDocument(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()
	record, err := app.FindRecordById("documents", id)
	if err != nil {
		t.Fatalf("reload document: %v", err)
	}
	return record
}

// An earlier document takes the checksum between the lookup and the save, so
// the scan falls back to marking; the unsaved checksum must not ride along.
func TestScanAllMarksDuplicateWhenChecksumIsTakenDuringSave(t *testing.T) {
	app := testpb.Open(t)
	userID := saveScanTestUser(t, app)
	checksum := scanTestChecksum(t)
	document := saveScanTestDocument(t, app, userID, "", "2026-01-02 10:00:00.000Z")

	var rival *core.Record
	app.OnRecordUpdate("documents").BindFunc(func(e *core.RecordEvent) error {
		if rival == nil && e.Record.Id == document.Id {
			rival = saveScanTestDocument(t, e.App, userID, checksum, "2026-01-01 10:00:00.000Z")
		}
		return e.Next()
	})

	result, err := ScanAll(app, config.Config{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rival == nil {
		t.Fatal("the race hook never ran")
	}
	if result.ExactMarked != 1 || result.FingerprintsFilled != 1 {
		t.Fatalf("expected one exact mark and one fingerprint, got %+v", result)
	}

	stored := reloadScanTestDocument(t, app, document.Id)
	if got := stored.GetString("duplicate_of"); got != rival.Id {
		t.Fatalf("duplicate_of = %q, want %q", got, rival.Id)
	}
	if got := stored.GetString("checksum"); got != "" {
		t.Fatalf("marked duplicate kept checksum %q", got)
	}
	if stored.GetString("text_fingerprint") == "" {
		t.Fatal("fingerprint was not backfilled")
	}
}

// A later document that wins the race still yields to the older one, the same
// as when the scan finds it first.
func TestScanAllKeepsChecksumOnTheOlderDocumentAfterARace(t *testing.T) {
	app := testpb.Open(t)
	userID := saveScanTestUser(t, app)
	checksum := scanTestChecksum(t)
	document := saveScanTestDocument(t, app, userID, "", "2026-01-01 10:00:00.000Z")

	var rival *core.Record
	app.OnRecordUpdate("documents").BindFunc(func(e *core.RecordEvent) error {
		if rival == nil && e.Record.Id == document.Id {
			rival = saveScanTestDocument(t, e.App, userID, checksum, "2026-01-02 10:00:00.000Z")
		}
		return e.Next()
	})

	result, err := ScanAll(app, config.Config{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rival == nil {
		t.Fatal("the race hook never ran")
	}
	if result.ExactMarked != 1 || result.ChecksumBackfilled != 1 {
		t.Fatalf("expected one exact mark and one backfill, got %+v", result)
	}
	if got := reloadScanTestDocument(t, app, document.Id).GetString("checksum"); got != checksum {
		t.Fatalf("older document checksum = %q, want %q", got, checksum)
	}
	stored := reloadScanTestDocument(t, app, rival.Id)
	if got := stored.GetString("duplicate_of"); got != document.Id {
		t.Fatalf("rival duplicate_of = %q, want %q", got, document.Id)
	}
	if got := stored.GetString("checksum"); got != "" {
		t.Fatalf("marked rival kept checksum %q", got)
	}
}

// A rolled-back ownership swap leaves the checksum with the later document; the
// fingerprint backfill must not retry the swap by saving it on the earlier one.
func TestScanAllDropsChecksumAfterFailedOwnershipSwap(t *testing.T) {
	app := testpb.Open(t)
	userID := saveScanTestUser(t, app)
	checksum := scanTestChecksum(t)
	document := saveScanTestDocument(t, app, userID, "", "2026-01-01 10:00:00.000Z")
	later := saveScanTestDocument(t, app, userID, checksum, "2026-01-02 10:00:00.000Z")

	failed := false
	app.OnRecordUpdate("documents").BindFunc(func(e *core.RecordEvent) error {
		if !failed && e.Record.Id == document.Id {
			failed = true
			return errors.New("injected save failure")
		}
		return e.Next()
	})

	if _, err := ScanAll(app, config.Config{}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !failed {
		t.Fatal("the failing hook never ran")
	}

	stored := reloadScanTestDocument(t, app, document.Id)
	if got := stored.GetString("checksum"); got != "" {
		t.Fatalf("checksum = %q after a rolled-back swap", got)
	}
	if stored.GetString("text_fingerprint") == "" {
		t.Fatal("fingerprint was not backfilled")
	}
	if got := reloadScanTestDocument(t, app, later.Id).GetString("checksum"); got != checksum {
		t.Fatalf("later document checksum = %q, want %q", got, checksum)
	}
}
