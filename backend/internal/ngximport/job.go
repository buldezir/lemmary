package ngximport

import (
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/importjob"
)

// Job statuses for in-memory async imports.
const (
	JobStatusRunning   = importjob.StatusRunning
	JobStatusCompleted = importjob.StatusCompleted
	JobStatusFailed    = importjob.StatusFailed
)

// Job is an in-memory import run snapshot (lost on process restart).
type Job = importjob.Job[Result]

var registry = importjob.NewRegistry[Result](importjob.DefaultRetention)

// Start begins an import in a background goroutine and returns the job id.
// Only one import may run at a time per owner.
func Start(app core.App, ownerUserID, baseURL, apiKey, mode string) (string, error) {
	return registry.Start(ownerUserID, func(func(done, total int)) (Result, error) {
		return runImport(app, ownerUserID, baseURL, apiKey, mode, nil)
	})
}

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) {
	return registry.Get(id)
}
