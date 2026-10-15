package archiveimport

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/testpb"
	// The shared schema template only includes what this package registered.
	_ "lemmary/backend/migrations"
)

func TestRemapIDsKeepsOnlyDocumentsRestoredWithIt(t *testing.T) {
	idByExported := map[string]string{"old_a": "new_a", "old_b": "new_b", "old_c": "new_c"}
	exported := []string{"old_b", "not_in_archive", "old_a", "old_c", "old_b"}
	if got := remapIDs(idByExported, "new_a", exported); got != `["new_b","new_c"]` {
		t.Fatalf("remapIDs = %s", got)
	}
	if got := remapIDs(idByExported, "new_a", []string{"not_in_archive"}); got != "" {
		t.Fatalf("remapIDs with no restored target = %q, want empty", got)
	}
}

// An archive from before not_duplicate_of carries a reviewed duplicate as
// completed with its link; restored, it goes back to review.
func TestApplyFixupsReturnsAReviewedDuplicateToReview(t *testing.T) {
	app := testpb.Open(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(users)
	user.Set("email", "restore@example.test")
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"original", "duplicate", "failed"} {
		file, err := filesystem.NewFileFromBytes([]byte(name), name+".txt")
		if err != nil {
			t.Fatal(err)
		}
		record := core.NewRecord(documents)
		record.Set("user", user.Id)
		record.Set("file", file)
		record.Set("processing_status", models.DocStatusCompleted)
		if name == "failed" {
			record.Set("processing_status", models.DocStatusFailed)
		}
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
		ids[name] = record.Id
	}

	applyFixups(app, []restoredDocument{
		{NewID: ids["original"], ExportedID: "old_original"},
		{NewID: ids["duplicate"], ExportedID: "old_duplicate", DuplicateOfExported: "old_original"},
		{NewID: ids["failed"], ExportedID: "old_failed", DuplicateOfExported: "old_original"},
	}, &Result{})

	for name, want := range map[string]string{
		"original":  models.DocStatusCompleted,
		"duplicate": models.DocStatusNeedsReview,
		"failed":    models.DocStatusFailed,
	} {
		stored, err := app.FindRecordById("documents", ids[name])
		if err != nil {
			t.Fatal(err)
		}
		if got := stored.GetString("processing_status"); got != want {
			t.Fatalf("%s: status %q, want %q", name, got, want)
		}
	}
}
