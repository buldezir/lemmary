package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/models"
)

type Processor struct {
	app        core.App
	rt         *config.Runtime
	processing sync.Mutex
}

func Register(app core.App, rt *config.Runtime, backfill *Backfiller) {
	p := &Processor{
		app: app,
		rt:  rt,
	}
	p.registerHooks()

	cronExpr := config.WorkerCronFromEnv()
	app.Cron().MustAdd("process_pending_jobs", cronExpr, func() {
		// Counted as in-flight work so a shutdown can wait for it. Cron jobs
		// are fired and forgotten, and nothing else would wait: with encryption
		// at rest on, a job still writing while the archive is sealed and the
		// working directory wiped loses everything it had done.
		defer inflight.Begin()()
		if err := p.processNextPending(); err != nil {
			app.Logger().Error("cron error", slog.Any("error", err))
		}
	})

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		p.recoverStaleRunningJobs()
		return e.Next()
	})

	// A second cron on the same expression: the job drain only ever reaches
	// documents a job was created for, and most of the documents that need
	// embedding never get one. The instance is passed in because the API binds
	// its manual sweep to the same one.
	registerEmbeddingBackfill(app, backfill)

	app.Logger().Info("worker registered", "cron", cronExpr)
}

// recoverStaleRunningJobs re-pends jobs a previous process left in "running".
// The pipeline runs outside any transaction, so a crash or restart mid-run
// strands the job (nextDueJob only picks pending) and its document in
// "processing" with no path back — bulk reprocess skips processing documents.
func (p *Processor) recoverStaleRunningJobs() {
	// Unfinished and not already queued, rather than status = running. A crash
	// during embed strands a job whose status apply_metadata already set to
	// completed, so a status test walks straight past the one window where the
	// pipeline spends real time -- and with the enqueue guard now keyed on
	// finished_at, such a job would block its document from ever being
	// reprocessed.
	jobs, err := p.app.FindRecordsByFilter(
		"processing_jobs",
		"finished_at = '' && status != {:pending}",
		"",
		0,
		0,
		map[string]any{"pending": models.JobStatusPending},
	)
	if err != nil {
		p.app.Logger().Error("list stale running jobs", slog.Any("error", err))
		return
	}
	for _, job := range jobs {
		job.Set("status", models.JobStatusPending)
		job.Set("next_attempt_at", "")
		if err := p.app.Save(job); err != nil {
			p.app.Logger().Error("re-pend stale running job", "job", job.Id, slog.Any("error", err))
			continue
		}
		p.app.Logger().Warn("re-pended job left running by a previous process", "job", job.Id)
	}
}

func (p *Processor) registerHooks() {
	p.app.OnRecordValidate("documents").BindFunc(func(e *core.RecordEvent) error {
		if err := validateDocumentNamedEntityOwnership(e.App, e.Record); err != nil {
			return err
		}
		return e.Next()
	})

	p.app.OnRecordCreate("documents").BindFunc(func(e *core.RecordEvent) error {
		record := e.Record
		if record.GetString("processing_status") == "" {
			record.Set("processing_status", models.DocStatusPending)
		}
		if err := duplicates.AssignChecksumFromUpload(e.App, record); err != nil {
			var dupErr *duplicates.ErrDuplicate
			if errors.As(err, &dupErr) {
				return router.NewBadRequestError(dupErr.Error(), map[string]any{
					"duplicate_of": dupErr.ExistingID,
				})
			}
			return err
		}
		if err := e.Next(); err != nil {
			if dupErr := duplicates.ErrDuplicateFromSaveConflict(e.App, record, err); dupErr != nil {
				return router.NewBadRequestError(dupErr.Error(), map[string]any{
					"duplicate_of": dupErr.ExistingID,
				})
			}
			return err
		}

		if skipsCreateJob(record) {
			return nil
		}

		steps := createStepsFor(record)
		_, err := createProcessingJob(e.App, record.Id, steps, nil)
		return err
	})

	p.app.OnRecordCreate("processing_jobs").BindFunc(func(e *core.RecordEvent) error {
		record := e.Record
		if record.GetString("task_id") == "" {
			record.Set("task_id", uuid.New().String())
		}
		steps, err := parseSteps(record)
		if err != nil {
			return err
		}
		if len(steps) == 0 {
			record.Set("steps", models.FullPipelineSteps)
		}
		return e.Next()
	})

	p.app.OnRecordAfterCreateSuccess("processing_jobs").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("status") == models.JobStatusPending {
			go p.drainPending()
		}
		return e.Next()
	})

	p.app.OnRecordAfterUpdateSuccess("processing_jobs").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("status") == models.JobStatusPending {
			go p.drainPending()
		}
		return e.Next()
	})
}

// Enqueue creates a pending job for documentID so the worker picks it up on the
// next drain. It is the entry point for callers outside this package (bulk
// reprocess); forceSteps may be nil.
func Enqueue(app core.App, documentID string, steps []string, forceSteps []string) (*core.Record, error) {
	return createProcessingJob(app, documentID, steps, forceSteps)
}

func createProcessingJob(app core.App, documentID string, steps []string, forceSteps []string) (*core.Record, error) {
	// Ensure-queued semantics: a document with an active job must not get a
	// second one — concurrent reprocess requests would otherwise run OCR and
	// AI extraction twice for the same document.
	//
	// Active is finished_at = '', not a status test. apply_metadata sets the
	// job's status to completed and saves it, and only then does embed run --
	// so for the whole of that window (eight seconds against a local sidecar,
	// longer on a backfill) a running job reads status=completed. Keying on
	// pending/running let a second pipeline in for exactly that window, and
	// the two then mutated the same document.
	//
	// Both terminal paths write finished_at: the end of PipelineRunner.Run and
	// failJob. A retry leaves it empty and re-pends, which is correct -- that
	// job is still active.
	existing, err := app.FindRecordsByFilter(
		"processing_jobs",
		"document = {:doc} && finished_at = ''",
		"-created",
		1,
		0,
		map[string]any{"doc": documentID},
	)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		app.Logger().Info("document already has an active job; not queueing another",
			"job", existing[0].Id,
			"document", documentID,
		)
		return existing[0], nil
	}

	jobsCollection, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		return nil, err
	}

	job := core.NewRecord(jobsCollection)
	job.Set("document", documentID)
	job.Set("status", models.JobStatusPending)
	job.Set("steps", steps)
	if len(forceSteps) > 0 {
		job.Set("force_steps", forceSteps)
	}

	if err := app.Save(job); err != nil {
		return nil, err
	}

	app.Logger().Info("created job",
		"job", job.Id,
		"document", documentID,
		"steps", steps,
		"task_id", job.GetString("task_id"),
	)
	return job, nil
}

func (p *Processor) drainPending() {
	if !p.processing.TryLock() {
		return
	}
	defer p.processing.Unlock()

	lastJobID := ""
	for {
		job, err := p.nextDueJob()
		if err != nil {
			p.app.Logger().Error("list pending jobs", slog.Any("error", err))
			return
		}
		if job == nil {
			return
		}

		snap := p.rt.Snapshot()
		if err := providersReady(snap); err != nil {
			// Leave the job pending so it runs once Settings are complete. Returning
			// (rather than retrying inline) is what keeps this from becoming a hot
			// loop on a fresh install where no provider is configured yet; the cron
			// re-enters drainPending on the next tick.
			p.app.Logger().Warn("pending jobs deferred; provider unavailable",
				"job", job.Id,
				slog.Any("error", err),
			)
			return
		}

		if job.Id == lastJobID {
			// runJob returned without moving the job out of the runnable set, so
			// picking it up again would spin. Hand it back to the cron instead.
			p.app.Logger().Error("job made no progress; deferring to next cron tick", "job", job.Id)
			return
		}
		lastJobID = job.Id

		if err := p.runJob(job.Id, snap); err != nil {
			p.app.Logger().Error("job error", "job", job.Id, slog.Any("error", err))
			// Push the job's next attempt back if it is still immediately due.
			// Without this, one persistently unclaimable job sits at the head of
			// nextDueJob forever and starves every job created after it.
			p.deferErroredJob(job.Id)
		}
	}
}

// deferErroredJob applies a short backoff to a job that errored while still
// pending and due, so the drain loop can move past it to younger jobs.
func (p *Processor) deferErroredJob(jobID string) {
	job, err := p.app.FindRecordById("processing_jobs", jobID)
	if err != nil {
		return
	}
	if job.GetString("status") != models.JobStatusPending {
		return
	}
	if at := job.GetString("next_attempt_at"); at != "" && at > nowTimestamp() {
		return
	}
	job.Set("next_attempt_at", timestampAfter(RetryDelay(1)))
	if err := p.app.Save(job); err != nil {
		p.app.Logger().Error("defer errored job", "job", jobID, slog.Any("error", err))
	}
}

// nextDueJob returns the oldest pending job whose backoff has elapsed, or nil.
func (p *Processor) nextDueJob() (*core.Record, error) {
	jobs, err := p.app.FindRecordsByFilter(
		"processing_jobs",
		"status = {:status} && (next_attempt_at = '' || next_attempt_at <= {:now})",
		"created",
		1,
		0,
		map[string]any{"status": models.JobStatusPending, "now": nowTimestamp()},
	)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	return jobs[0], nil
}

// providersReady reports whether the snapshot has everything a job needs.
func providersReady(snap config.Snapshot) error {
	if snap.OCR == nil {
		return fmt.Errorf("OCR provider is not configured; update Settings")
	}
	if snap.AI == nil {
		return fmt.Errorf("AI extractor is not configured; update Settings")
	}
	return nil
}

func (p *Processor) processNextPending() error {
	p.drainPending()
	return nil
}

func (p *Processor) runJob(jobID string, snap config.Snapshot) error {
	claimed := false
	err := p.app.RunInTransaction(func(txApp core.App) error {
		job, err := txApp.FindRecordById("processing_jobs", jobID)
		if err != nil {
			return err
		}
		if job.GetString("status") != models.JobStatusPending {
			return nil
		}

		steps, err := parseSteps(job)
		if err != nil {
			return err
		}
		if len(steps) == 0 {
			return fmt.Errorf("job %s has no steps", jobID)
		}

		document, err := txApp.FindRecordById("documents", job.GetString("document"))
		if err != nil {
			return err
		}

		claimed = true
		p.app.Logger().Info("picked job",
			"job", job.Id,
			"document", document.Id,
			"steps", steps,
		)

		job.Set("status", models.JobStatusRunning)
		if job.GetString("started_at") == "" {
			job.Set("started_at", nowTimestamp())
		}

		runs, err := parseStepRuns(job)
		if err != nil {
			return err
		}
		runs = syncStepRuns(steps, runs)
		saveStepRuns(job, runs)

		document.Set("processing_status", models.DocStatusProcessing)
		if err := txApp.Save(document); err != nil {
			return err
		}
		return txApp.Save(job)
	})
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	runner := NewPipelineRunner(p.app, snap.Cfg, snap.OCR, snap.AI, snap.Embedder)
	return runner.Run(context.Background(), jobID)
}

func parseSteps(job *core.Record) ([]string, error) {
	raw := job.Get("steps")
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		steps := make([]string, 0, len(v))
		for _, item := range v {
			name, ok := item.(string)
			if !ok || name == "" {
				continue
			}
			steps = append(steps, name)
		}
		return steps, nil
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal steps: %w", err)
		}
		var steps []string
		if err := json.Unmarshal(data, &steps); err != nil {
			return nil, fmt.Errorf("unmarshal steps: %w", err)
		}
		return steps, nil
	}
}

func parseForceSteps(job *core.Record) map[string]bool {
	forced := make(map[string]bool)
	raw := job.Get("force_steps")
	if raw == nil {
		return forced
	}

	var names []string
	data, err := json.Marshal(raw)
	if err != nil {
		return forced
	}
	if err := json.Unmarshal(data, &names); err != nil {
		return forced
	}
	for _, name := range names {
		if name != "" {
			forced[name] = true
		}
	}
	return forced
}
