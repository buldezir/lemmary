package appapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/pocketbase/dbx"
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
	// Kept is documents the sweep deliberately spared: queued, but already
	// through the pipeline once. Separate from Remaining so the page can say
	// "kept" rather than report protection as a failure.
	Kept int `json:"kept"`
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
		if errors.Is(err, sql.ErrNoRows) {
			// The document went away under us -- a parallel discard. The job is
			// already settled, and the cascade will take it.
			stopped = true
			return nil
		}
		if err != nil {
			// Anything other than a gone document rolls the job write back. A
			// lock or a timeout here must not leave a cancelled job sitting over
			// a document the page still shows as queued.
			return err
		}
		// Only a document that is itself still waiting becomes cancelled. A
		// crash during embed re-pends a job whose document apply_metadata has
		// already written "completed" (recoverStaleRunningJobs), and stamping
		// cancelled over that would hand a processed document to the discard
		// sweep below.
		if document.GetString("processing_status") == models.DocStatusPending {
			document.Set("processing_status", models.DocStatusCancelled)
			if err := txApp.Save(document); err != nil {
				return err
			}
		}
		stopped = true
		return nil
	})
	return stopped, err
}

// handlePostDiscardUnprocessed deletes the caller's documents that are still
// queued or were deliberately cancelled, and have never been through the
// pipeline.
//
// Status alone cannot decide that. Failed is excluded because a document can
// have processed successfully before a later reprocess fails, and "pending" has
// the same problem from the other side: reprocess.queueOne flips a completed
// document back to pending before enqueueing it, so the queue holds library
// documents as well as fresh uploads. wasProcessed is what tells them apart.
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
				result.Kept++
			}
		}

		app.Logger().Info("unprocessed documents discarded",
			slog.String("owner", ownerID),
			slog.Int("deleted", result.Deleted),
			slog.Int("kept", result.Kept),
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
			processed, err := wasProcessed(txApp, document)
			if err != nil {
				return err
			}
			if processed {
				return nil
			}
			if err := txApp.Delete(document); err != nil {
				return err
			}
			deleted = true
		}
		return nil
	})
	return deleted, err
}

// wasProcessed reports whether the document has ever come out of the pipeline
// with something to lose, and so must survive the discard sweep whatever its
// current status says.
//
// Two signals, because neither covers the other. OCR text is the cheap one and
// catches the common case -- a completed document requeued by reprocess, or one
// whose extraction is retrying after OCR already succeeded, both of which sit at
// "pending" with their text intact. A finished job is the backstop for the
// document OCR legitimately found no words in: empty text, still processed,
// still not ours to delete. Cancelled jobs do not count towards it -- stopping a
// fresh import leaves exactly that, and those are what this sweep is for.
func wasProcessed(app core.App, document *core.Record) (bool, error) {
	if document.GetString("ocr_text") != "" {
		return true, nil
	}
	// processing_jobs is never pruned, so an old job is proof that stays proof.
	finished, err := app.CountRecords(
		"processing_jobs",
		dbx.NewExp(
			"document = {:document} AND finished_at != '' AND status != {:cancelled}",
			dbx.Params{"document": document.Id, "cancelled": models.JobStatusCancelled},
		),
	)
	if err != nil {
		return false, err
	}
	return finished > 0, nil
}
