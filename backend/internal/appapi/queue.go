package appapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/models"
)

// stopReason lands in the job's error field, so a stopped job says why it is
// sitting in Activity's "Recently cancelled" group instead of showing nothing.
const stopReason = "Stopped from the Activity page before it ran."

type stopQueueResult struct {
	Stopped int `json:"stopped"`
	// Running is what could not be stopped: the job already inside the
	// pipeline, which runs to the end. Zero or one in practice -- the worker
	// drains serially -- but counted rather than assumed.
	Running int `json:"running"`
	// Remaining is pending work a save error prevented us from cancelling.
	Remaining int `json:"remaining"`
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
// Stopped jobs and documents are marked cancelled. This keeps deliberate user
// action out of failure counts and gives the discard sweep an exact category
// that cannot include a previously processed document whose reprocess failed.
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
				result.Remaining++
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
			slog.Int("remaining", result.Remaining),
		)
		return writeJSON(e, http.StatusOK, result)
	}
}

// stopJob settles one pending job and its document, reporting false when the
// worker claimed it first.
//
// The re-read inside the transaction is that check: the drain loop claims a job
// by flipping it to running in a transaction of its own, and without this a
// stop that raced the claim would write "cancelled" over a job that is at that
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

		fresh.Set("status", models.JobStatusCancelled)
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
		document.Set("processing_status", models.DocStatusCancelled)
		if err := txApp.Save(document); err != nil {
			return err
		}
		stopped = true
		return nil
	})
	return stopped, err
}

// handlePostDiscardUnprocessed deletes the caller's documents that are still
// queued or were deliberately cancelled.
//
// Failed documents are deliberately excluded: a document can have processed
// successfully before a later reprocess fails, so failed does not mean it is
// safe to throw the original away.
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

		// ponytail: sweeps the whole set in one request, so a library with tens
		// of thousands of cancelled documents deletes for as long as the client
		// will wait.
		// Page it (or hand it to importjob's registry) if that ever happens.
		documents, err := app.FindRecordsByFilter(
			"documents",
			"user = {:user} && (processing_status = {:pending} || processing_status = {:cancelled})",
			"created",
			0,
			0,
			map[string]any{
				"user":      ownerID,
				"pending":   models.DocStatusPending,
				"cancelled": models.DocStatusCancelled,
			},
		)
		if err != nil {
			app.Logger().Error("list unprocessed documents", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Could not read the unprocessed documents.")
		}

		result := discardResult{}
		for _, document := range documents {
			deleted, err := discardDocument(app, document.Id, ownerID)
			if err != nil {
				app.Logger().Error("discard document", "document", document.Id, slog.Any("error", err))
				result.Remaining++
				continue
			}
			if deleted {
				result.Deleted++
			} else {
				result.Remaining++
			}
		}

		app.Logger().Info("unprocessed documents discarded",
			slog.String("owner", ownerID),
			slog.Int("deleted", result.Deleted),
			slog.Int("remaining", result.Remaining),
		)
		return writeJSON(e, http.StatusOK, result)
	}
}

// discardDocument checks and deletes in one transaction. The worker claims a
// pending job in its own transaction and flips the document to processing; the
// two transactions therefore serialize, so this can never delete a document
// after the worker has begun using its file.
func discardDocument(app core.App, documentID, ownerID string) (bool, error) {
	deleted := false
	err := app.RunInTransaction(func(txApp core.App) error {
		document, err := txApp.FindRecordById("documents", documentID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if document.GetString("user") != ownerID {
			return nil
		}
		switch document.GetString("processing_status") {
		case models.DocStatusPending, models.DocStatusCancelled:
			if err := txApp.Delete(document); err != nil {
				return err
			}
			deleted = true
		}
		return nil
	})
	return deleted, err
}
