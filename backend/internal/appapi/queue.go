package appapi

import (
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/models"
)

// stopReason lands in the job's error field, so a stopped job says why it is
// sitting in Activity's "Recently failed" group instead of showing nothing.
const stopReason = "Stopped from the Activity page before it ran."

type stopQueueResult struct {
	Stopped int `json:"stopped"`
	// Running is what could not be stopped: the job already inside the
	// pipeline, which runs to the end. Zero or one in practice -- the worker
	// drains serially -- but counted rather than assumed.
	Running int `json:"running"`
}

type discardResult struct {
	Deleted int `json:"deleted"`
	// Remaining is what a delete error left behind, so the page can say the
	// sweep was partial rather than report a clean number it did not achieve.
	Remaining int `json:"remaining"`
}

// handlePostStopQueue cancels every job of the caller's that has not started.
//
// It stops the queue, not the pipeline: the worker drains serially inside
// runner.Run with no cancellation channel to pull, so the job in flight runs to
// the end and drainPending then finds nothing left to pick up. That is the
// whole of "stop" here, and it is enough for the case it exists for -- an
// archive dropped in by mistake, where the cost is the four hundred documents
// behind the current one, not the current one.
//
// Stopped jobs are marked failed rather than given a status of their own.
// "cancelled" would mean a migration, a fifth badge, a fifth thing every status
// filter has to know -- for no behaviour the user wants that failed does not
// already give them: the documents land under "Recently failed" with the
// Reprocess button already beside them, and Management's bulk reprocess sees
// them too.
func handlePostStopQueue(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		// finished_at = '' as well as the status test, for the reason
		// createProcessingJob gives: status alone does not mark the end of a
		// job's life, finished_at does.
		jobs, err := app.FindRecordsByFilter(
			"processing_jobs",
			"finished_at = '' && document.user = {:owner}",
			"created",
			0,
			0,
			map[string]any{"owner": ownerID},
		)
		if err != nil {
			app.Logger().Error("list jobs to stop", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Could not read the queue.")
		}

		result := stopQueueResult{}
		for _, job := range jobs {
			if job.GetString("status") != models.JobStatusPending {
				result.Running++
				continue
			}
			stopped, err := stopJob(app, job)
			if err != nil {
				app.Logger().Error("stop job", "job", job.Id, slog.Any("error", err))
				continue
			}
			if stopped {
				result.Stopped++
			} else {
				result.Running++
			}
		}

		app.Logger().Info("queue stopped by hand",
			slog.String("owner", ownerID),
			slog.Int("stopped", result.Stopped),
			slog.Int("running", result.Running),
		)
		return writeJSON(e, http.StatusOK, result)
	}
}

// stopJob settles one pending job and its document, reporting false when the
// worker claimed it first.
//
// The re-read inside the transaction is that check: the drain loop claims a job
// by flipping it to running in a transaction of its own, and without this a
// stop that raced the claim would write "failed" over a job that is at that
// moment running OCR -- which the pipeline would then overwrite again at the
// end, leaving the Activity page contradicting itself in between.
func stopJob(app core.App, job *core.Record) (bool, error) {
	stopped := false
	err := app.RunInTransaction(func(txApp core.App) error {
		fresh, err := txApp.FindRecordById("processing_jobs", job.Id)
		if err != nil {
			return err
		}
		if fresh.GetString("status") != models.JobStatusPending {
			return nil
		}

		fresh.Set("status", models.JobStatusFailed)
		fresh.Set("finished_at", types.NowDateTime())
		fresh.Set("error", stopReason)
		if err := txApp.Save(fresh); err != nil {
			return err
		}

		document, err := txApp.FindRecordById("documents", fresh.GetString("document"))
		if err != nil {
			// The document went away under us -- a parallel discard. The job is
			// already settled, and the cascade will take it.
			stopped = true
			return nil
		}
		document.Set("processing_status", models.DocStatusFailed)
		if err := txApp.Save(document); err != nil {
			return err
		}
		stopped = true
		return nil
	})
	return stopped, err
}

// handlePostDiscardUnprocessed deletes the caller's documents that have never
// been through the pipeline: everything queued and everything failed.
//
// Failed as well as queued, because the two buttons are used in that order --
// stop the queue, then throw away what it was about to work on -- and stopping
// is what puts those documents on "failed". Documents that processed, including
// ones waiting to be reviewed, are never touched.
//
// Deleting the document is also what stops its job: processing_jobs.document
// cascades, so a pending job disappears with the document rather than being
// left to fail on a record that is gone.
func handlePostDiscardUnprocessed(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		// "processing" is left out on purpose: that is the one document inside
		// the pipeline, and deleting it out from under a running step is how
		// you get a half-written file and a stack trace.
		//
		// ponytail: sweeps the whole set in one request, so a library with tens
		// of thousands of failures deletes for as long as the client will wait.
		// Page it (or hand it to importjob's registry) if that ever happens.
		documents, err := app.FindRecordsByFilter(
			"documents",
			"user = {:user} && (processing_status = {:pending} || processing_status = {:failed})",
			"created",
			0,
			0,
			map[string]any{
				"user":    ownerID,
				"pending": models.DocStatusPending,
				"failed":  models.DocStatusFailed,
			},
		)
		if err != nil {
			app.Logger().Error("list unprocessed documents", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Could not read the unprocessed documents.")
		}

		result := discardResult{}
		for _, document := range documents {
			if err := app.Delete(document); err != nil {
				app.Logger().Error("discard document", "document", document.Id, slog.Any("error", err))
				result.Remaining++
				continue
			}
			result.Deleted++
		}

		app.Logger().Info("unprocessed documents discarded",
			slog.String("owner", ownerID),
			slog.Int("deleted", result.Deleted),
			slog.Int("remaining", result.Remaining),
		)
		return writeJSON(e, http.StatusOK, result)
	}
}
