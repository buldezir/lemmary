package worker

import (
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/testpb"
)

func TestDetectDuplicatesShouldSkipWithoutOCR(t *testing.T) {
	docs := coreTestDocumentsCollection()
	doc := core.NewRecord(docs)

	step := &DetectDuplicatesStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("expected skip without ocr text")
	}
}

func TestDetectDuplicatesShouldNotSkipWithOCR(t *testing.T) {
	docs := coreTestDocumentsCollection()
	doc := core.NewRecord(docs)
	doc.Set("ocr_text", "enough text to process")

	step := &DetectDuplicatesStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if skipped {
		t.Fatal("expected not to skip when ocr text exists")
	}
	if state.OCRText != "enough text to process" {
		t.Fatalf("OCRText=%q", state.OCRText)
	}
}

func TestExtractMetadataShouldSkipWhenDuplicateOfSet(t *testing.T) {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(&core.TextField{Name: "duplicate_of"})
	doc := core.NewRecord(docs)
	doc.Set("duplicate_of", "originaldoc0001")

	step := &ExtractMetadataStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("expected skip when duplicate_of is set")
	}
}

func TestApplyMetadataShouldSkipWhenDuplicateOfSet(t *testing.T) {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(&core.TextField{Name: "duplicate_of"})
	doc := core.NewRecord(docs)
	doc.Set("duplicate_of", "originaldoc0001")

	step := &ApplyMetadataStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("expected skip when duplicate_of is set")
	}
}

func TestFingerprintHelpersUsedByDetectStep(t *testing.T) {
	text := strings.Repeat("sample document text for fingerprinting purposes ", 4)
	fp := duplicates.FingerprintHex(text)
	if fp == "" {
		t.Fatal("expected fingerprint")
	}
}

func saveReviewTestDocument(t *testing.T, app core.App, userID, title, duplicateOf string) *core.Record {
	t.Helper()
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	file, err := filesystem.NewFileFromBytes([]byte("document "+title), "doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(documents)
	record.Set("user", userID)
	record.Set("file", file)
	record.Set("title", title)
	record.Set("duplicate_of", duplicateOf)
	record.Set("processing_status", models.DocStatusNeedsReview)
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	return record
}

// Loaded first, as every caller loads it: Original() is the row as read, and
// a record only saved in memory still reads as new.
func markReviewTestDocumentReviewed(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()
	record, err := app.FindRecordById("documents", id)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("processing_status", models.DocStatusCompleted)
	if err := app.Save(record); err != nil {
		t.Fatalf("mark reviewed: %v", err)
	}
	if record, err = app.FindRecordById("documents", id); err != nil {
		t.Fatal(err)
	}
	return record
}

func reviewTestJobSteps(t *testing.T, app core.App, documentID string) []string {
	t.Helper()
	jobs, err := app.FindRecordsByFilter("processing_jobs", "document = {:doc}", "", 0, 0, map[string]any{"doc": documentID})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		return nil
	}
	if len(jobs) > 1 {
		t.Fatalf("%d jobs queued, want one", len(jobs))
	}
	return jobs[0].GetStringSlice("steps")
}

// The run that marked it sets needs_review before it finishes, and finishing
// would save the link back over the review, so the review waits for it.
func TestMarkingADuplicateReviewedWaitsForItsJob(t *testing.T) {
	app := testpb.Open(t)
	app.OnRecordUpdate("documents").BindFunc(keepReviewedDuplicate)
	userID := createTagUser(t, app, "review@example.test")
	original := saveReviewTestDocument(t, app, userID, "Invoice", "")
	duplicate := saveReviewTestDocument(t, app, userID, "", original.Id)
	makeJob(t, app, duplicate.Id, models.JobStatusRunning, "")

	record, err := app.FindRecordById("documents", duplicate.Id)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("processing_status", models.DocStatusCompleted)
	if err := app.Save(record); err == nil {
		t.Fatal("review saved while the job that marked the duplicate is still open")
	}
	stored, err := app.FindRecordById("documents", duplicate.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetString("duplicate_of") != original.Id || stored.GetString("processing_status") != models.DocStatusNeedsReview {
		t.Fatalf("duplicate_of=%q status=%q after a refused review", stored.GetString("duplicate_of"), stored.GetString("processing_status"))
	}
}

// Reviewing a possible duplicate keeps it: the pair is remembered apart and
// the steps a duplicate skipped are queued, extraction only for a document
// nobody has titled yet.
func TestMarkingADuplicateReviewedKeepsItApart(t *testing.T) {
	app := testpb.Open(t)
	app.OnRecordUpdate("documents").BindFunc(keepReviewedDuplicate)
	userID := createTagUser(t, app, "review@example.test")
	original := saveReviewTestDocument(t, app, userID, "Invoice", "")

	for _, tc := range []struct {
		title string
		steps []string
	}{
		{"", models.ExtractionPipelineSteps},
		{"Invoice, second copy", []string{models.StepEmbed, models.StepLinkRelated}},
	} {
		kept := markReviewTestDocumentReviewed(t, app, saveReviewTestDocument(t, app, userID, tc.title, original.Id).Id)
		if kept.GetString("duplicate_of") != "" || !slices.Equal(kept.GetStringSlice("not_duplicate_of"), []string{original.Id}) {
			t.Fatalf("%q: duplicate_of=%q not_duplicate_of=%v", tc.title, kept.GetString("duplicate_of"), kept.GetStringSlice("not_duplicate_of"))
		}
		if got := kept.GetString("processing_status"); got != models.DocStatusPending {
			t.Fatalf("%q: status %q, want pending", tc.title, got)
		}
		if got := reviewTestJobSteps(t, app, kept.Id); !slices.Equal(got, tc.steps) {
			t.Fatalf("%q: queued %v, want %v", tc.title, got, tc.steps)
		}
	}

	// A reprocess that failed keeps duplicate_of; once edited by hand, the
	// failed copy is reviewable and kept apart the same way.
	failed := saveReviewTestDocument(t, app, userID, "Invoice, fixed by hand", original.Id)
	failed.Set("processing_status", models.DocStatusFailed)
	failed.Set("metadata_source", models.MetadataSourceUser)
	if err := app.Save(failed); err != nil {
		t.Fatal(err)
	}
	kept := markReviewTestDocumentReviewed(t, app, failed.Id)
	if kept.GetString("duplicate_of") != "" || kept.GetString("processing_status") != models.DocStatusPending {
		t.Fatalf("failed copy: duplicate_of=%q status=%q", kept.GetString("duplicate_of"), kept.GetString("processing_status"))
	}
	if got := reviewTestJobSteps(t, app, kept.Id); !slices.Equal(got, []string{models.StepEmbed, models.StepLinkRelated}) {
		t.Fatalf("failed copy queued %v", got)
	}

	reviewed := markReviewTestDocumentReviewed(t, app, original.Id)
	if got := reviewed.GetString("processing_status"); got != models.DocStatusCompleted {
		t.Fatalf("a document that is no duplicate: status %q, want completed", got)
	}
	if steps := reviewTestJobSteps(t, app, original.Id); steps != nil {
		t.Fatalf("a document that is no duplicate queued %v", steps)
	}
}
