package worker

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

// EnqueueEditedRules queues the owner's edited-trigger rules for a document
// someone just changed by hand. Only a settled document: one still being
// processed gets its rules from that run, and a failed one has no metadata to
// read. The job hands the document back in the status it has now.
func EnqueueEditedRules(app core.App, document *core.Record) error {
	status := document.GetString("processing_status")
	if status != models.DocStatusCompleted && status != models.DocStatusNeedsReview {
		return nil
	}
	if document.GetString("duplicate_of") != "" {
		return nil
	}
	list, err := rules.Load(app, document.GetString("user"), rules.TriggerEdited)
	if err != nil || len(list) == 0 {
		return err
	}
	return app.RunInTransaction(func(txApp core.App) error {
		active, err := txApp.CountRecords("processing_jobs", dbx.HashExp{"document": document.Id, "finished_at": ""})
		if err != nil || active > 0 {
			return err
		}
		job, err := createProcessingJob(txApp, document.Id, models.EditedRulesSteps, nil, config.Overrides{})
		if err != nil {
			return err
		}
		job.Set(models.JobReturnStatus, status)
		return txApp.Save(job)
	})
}
