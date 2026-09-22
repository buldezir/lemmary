package worker

import (
	"context"
	"log/slog"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

// Review mode: a proposal naming an existing tag is assigned like any tag, an
// invented name (wherever the model put it) lands on the job for the reviewer,
// and the document gets no tag that does not exist. Off, proposals vanish.
func TestApplyMetadataRoutesSuggestedTags(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")
	invoices := createTag(t, app, "Invoices", owner)

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, review bool) (*core.Record, *models.ExtractedMetadata) {
		doc := core.NewRecord(documents)
		doc.Set("user", owner)
		doc.Set("title", "pending")
		file, err := filesystem.NewFileFromBytes([]byte("x"), "x.txt")
		if err != nil {
			t.Fatal(err)
		}
		doc.Set("file", file)
		if err := app.Save(doc); err != nil {
			t.Fatalf("save document: %v", err)
		}
		job := core.NewRecord(jobs)
		job.Set("document", doc.Id)
		job.Set("status", models.JobStatusRunning)
		job.Set("steps", []string{models.StepApplyMetadata})

		state := &StepState{
			App:      app,
			Cfg:      config.Config{AlwaysRequireReview: review},
			Job:      job,
			Document: doc,
			Logger:   slog.Default(),
			Metadata: &models.ExtractedMetadata{
				Title:         "Boiler warranty",
				Confidence:    0.9,
				Tags:          []string{"Heating"},              // invented inside the closed field
				SuggestedTags: []string{"invoices", "Warranty"}, // one exists, one is new
			},
		}
		if err := (&ApplyMetadataStep{}).Run(context.Background(), state); err != nil {
			t.Fatalf("apply: %v", err)
		}
		stored, err := app.FindRecordById("processing_jobs", job.Id)
		if err != nil {
			t.Fatalf("the job must be saved before the document wakes the reviewer's page: %v", err)
		}
		saved, err := loadMetadataJSON(stored)
		if err != nil {
			t.Fatal(err)
		}
		return doc, saved
	}

	t.Run("review on", func(t *testing.T) {
		doc, saved := run(t, true)
		if got := doc.GetStringSlice("tags"); len(got) != 1 || got[0] != invoices {
			t.Fatalf("document tags = %v, want only the existing Invoices tag %q", got, invoices)
		}
		want := []string{"Heating", "Warranty"}
		if len(saved.SuggestedTags) != len(want) || saved.SuggestedTags[0] != want[0] || saved.SuggestedTags[1] != want[1] {
			t.Fatalf("job suggested_tags = %v, want %v", saved.SuggestedTags, want)
		}
	})

	t.Run("review off", func(t *testing.T) {
		doc, saved := run(t, false)
		if got := doc.GetStringSlice("tags"); len(got) != 0 {
			t.Fatalf("document tags = %v, want none: suggestions are not read when nobody reviews", got)
		}
		if len(saved.SuggestedTags) != 0 {
			t.Fatalf("job suggested_tags = %v, want none", saved.SuggestedTags)
		}
	})
}
