package worker

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/testpb"
	// Registers the migrations that create processing_jobs; without them
	// the shared schema template is empty.
	_ "lemmary/backend/migrations"
)

func bootAppForEnqueue(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	return testpb.Open(t)
}

func makeDocumentForEnqueue(t *testing.T, app core.App) string {
	t.Helper()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(users)
	user.Set("email", "enqueue@example.test")
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	document := core.NewRecord(documents)
	document.Set("user", user.Id)
	document.Set("title", "Enqueue guard")
	file, err := filesystem.NewFileFromBytes([]byte("enqueue guard"), "guard.txt")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	document.Set("file", file)
	if err := app.Save(document); err != nil {
		t.Fatalf("save document: %v", err)
	}
	return document.Id
}

func makeJob(t *testing.T, app core.App, documentID, status, finishedAt string) *core.Record {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatalf("processing_jobs collection: %v", err)
	}
	job := core.NewRecord(collection)
	job.Set("document", documentID)
	job.Set("status", status)
	job.Set("steps", []string{models.StepEmbed})
	job.Set("started_at", "2026-09-04 15:11:35.176Z")
	job.Set("finished_at", finishedAt)
	if err := app.Save(job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	return job
}

// The window this guard exists for: apply_metadata marks the job completed
// before embed runs, so a guard keyed on pending/running would let a second
// pipeline in over the same document.
func TestEnqueueRefusesASecondJobWhileEmbedIsStillRunning(t *testing.T) {
	app := bootAppForEnqueue(t)
	documentID := makeDocumentForEnqueue(t, app)

	live := makeJob(t, app, documentID, models.JobStatusCompleted, "")

	got, err := createProcessingJob(app, documentID, []string{models.StepEmbed}, nil, config.Overrides{})
	if err != nil {
		t.Fatalf("createProcessingJob: %v", err)
	}
	if got.Id != live.Id {
		t.Fatalf("queued a second job %s alongside the live one %s", got.Id, live.Id)
	}
}

// The states either side of it, so the guard does not simply refuse everything.
func TestEnqueueGuardBoundaries(t *testing.T) {
	cases := map[string]struct {
		status     string
		finishedAt string
		wantReuse  bool
	}{
		"pending is active":             {models.JobStatusPending, "", true},
		"running is active":             {models.JobStatusRunning, "", true},
		"completed mid-embed is active": {models.JobStatusCompleted, "", true},
		"completed and finished is not": {models.JobStatusCompleted, "2026-09-04 15:11:50.117Z", false},
		"failed and finished is not":    {models.JobStatusFailed, "2026-09-04 15:11:50.117Z", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			app := bootAppForEnqueue(t)
			documentID := makeDocumentForEnqueue(t, app)
			existing := makeJob(t, app, documentID, tc.status, tc.finishedAt)

			got, err := createProcessingJob(app, documentID, []string{models.StepEmbed}, nil, config.Overrides{})
			if err != nil {
				t.Fatalf("createProcessingJob: %v", err)
			}
			if reused := got.Id == existing.Id; reused != tc.wantReuse {
				t.Fatalf("reused existing job = %v, want %v", reused, tc.wantReuse)
			}
		})
	}
}
