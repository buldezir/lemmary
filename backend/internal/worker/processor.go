package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/metrics"
	"lemmary/backend/internal/models"
)

type Processor struct {
	app core.App
	rt  *config.Runtime
	// limit is how many pipelines may run at once. It replaces a plain mutex:
	// a document is mostly time spent waiting on somebody else's HTTP server,
	// so one at a time left the machine idle for hours on a bulk import.
	limit int

	mu     sync.Mutex
	active int
	// inflight is the jobs a live drain goroutine has picked but not yet
	// claimed in the database. Without it every drain would select the same
	// globally oldest row, and the losers of the claim transaction would spin
	// on a job they can never have.
	inflight map[string]struct{}

	// snapshot is the published configuration, rt.Snapshot in production. A
	// field because a drain test has to hand the pipeline stub providers, and
	// a Runtime only ever publishes what it built from the settings record.
	snapshot func() config.Snapshot
}

func Register(app core.App, rt *config.Runtime, backfill *Backfiller, concurrency int) {
	if concurrency < 1 {
		concurrency = 1
	}
	p := &Processor{
		app:      app,
		rt:       rt,
		limit:    concurrency,
		inflight: make(map[string]struct{}),
	}
	p.snapshot = rt.Snapshot
	p.registerHooks()

	cronExpr := config.WorkerCronFromEnv()
	app.Cron().MustAdd("process_pending_jobs", cronExpr, func() {
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

	// Read on scrape rather than tracked, so nothing has to stay in step with
	// a queue three hooks and a cron all push into.
	// ponytail: one COUNT per scrape. If a scrape interval ever makes that
	// matter, keep the number in the Processor and update it where jobs are
	// created and claimed.
	reg := metrics.QueueDepth(func() (int64, error) {
		return app.CountRecords("processing_jobs", dbx.HashExp{"status": models.JobStatusPending})
	})
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		_ = reg.Unregister()
		return e.Next()
	})

	app.Logger().Info("worker registered", "cron", cronExpr, "concurrency", concurrency)
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
		_, err := createProcessingJob(e.App, record.Id, steps, nil, config.Overrides{})
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
		// The trust boundary for the provider/model choices: this collection is
		// writable by the document's owner, so `overrides` arrives from a
		// browser on the single-document reprocess path as well as through the
		// custom endpoint. Refused as a bad request, which is what the direct
		// collection write surfaces to the form that made it.
		if err := validateJobOverrides(e.App, p.rt.Snapshot().Cfg, record); err != nil {
			return router.NewBadRequestError(err.Error(), nil)
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
// reprocess); forceSteps may be nil and overrides may be zero, which means the
// job runs on the bindings in Settings.
func Enqueue(app core.App, documentID string, steps []string, forceSteps []string, overrides config.Overrides) (*core.Record, error) {
	return createProcessingJob(app, documentID, steps, forceSteps, overrides)
}

func createProcessingJob(app core.App, documentID string, steps []string, forceSteps []string, overrides config.Overrides) (*core.Record, error) {
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
	setJobOverrides(job, overrides)

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

// takeSlot reserves one of the p.limit concurrent pipeline slots, or reports
// that they are all busy. Same shape as the TryLock it replaces, with N tokens.
func (p *Processor) takeSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active >= p.limit {
		return false
	}
	p.active++
	return true
}

func (p *Processor) releaseSlot() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

// releaseJob drops a job from the picked-but-not-yet-claimed set.
func (p *Processor) releaseJob(jobID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inflight, jobID)
}

// fanOut runs one drain per slot and waits for them.
//
// The hooks on processing_jobs already start a drain per job, so the upload
// path fans out on its own. This is for the paths that only the cron reaches --
// a restart with a full queue, or an install where the provider was missing
// when the jobs were made -- which a single drain would work through serially.
func (p *Processor) fanOut() {
	var wg sync.WaitGroup
	for i := 0; i < p.limit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.drainPending()
		}()
	}
	wg.Wait()
}

func (p *Processor) drainPending() {
	if !p.takeSlot() {
		return
	}
	defer p.releaseSlot()

	// Counted as in-flight work so a shutdown can wait for it. Nothing else
	// would: cron jobs are fired and forgotten, and the record hooks start this
	// with a bare `go`. With encryption at rest on, a job still writing while
	// the archive is sealed and the working directory wiped loses everything it
	// had done. This sits here rather than in the cron closure because the hook
	// callers are the common path and were never counted at all.
	defer inflight.Begin()()

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

		// Checked before anything else touches the job, so every path below
		// that leaves it runnable is caught here rather than spinning. It used
		// to sit lower, which was safe only because both paths above it
		// returned; the override paths do not.
		if job.Id == lastJobID {
			// The previous iteration returned without moving the job out of the
			// runnable set, so picking it up again would spin. Hand it back to
			// the cron instead.
			p.app.Logger().Error("job made no progress; deferring to next cron tick", "job", job.Id)
			p.releaseJob(job.Id)
			return
		}
		lastJobID = job.Id

		if !p.attempt(job) {
			return
		}
	}
}

// attempt runs one picked job and reports whether the drain should keep going.
// It exists so the job's release from p.inflight can be deferred over every
// path out, including the ones that fail before the pipeline starts.
func (p *Processor) attempt(job *core.Record) bool {
	defer p.releaseJob(job.Id)

	// The job's own bindings are applied before readiness is judged, not
	// after. A job queued with an OCR or extraction override may be the one
	// job that *can* run on an instance whose configured provider is gone,
	// and asking providersReady about the configured snapshot would defer it
	// forever over a binding it does not use.
	snap, err := p.effectiveSnapshot(job)
	if err != nil {
		// Stored overrides that no longer resolve -- a provider deleted
		// between queueing and draining. Failed rather than deferred: the
		// job asked for a provider that no longer exists, and no amount of
		// waiting brings it back. The document lands on "failed", where a
		// plain reprocess can pick it up again without the override.
		//
		// Return value dropped deliberately: failJob hands back the error it
		// was given whether or not the write succeeded, so testing it would
		// log the same error twice under a message about the write. failJob
		// logs "job failed" with the cause itself.
		_ = failJob(p.app, job, nil, err)
		return true
	}
	if err := providersReady(snap); err != nil {
		// Leave the job pending so it runs once Settings are complete. Returning
		// (rather than retrying inline) is what keeps this from becoming a hot
		// loop on a fresh install where no provider is configured yet; the cron
		// re-enters drainPending on the next tick.
		p.app.Logger().Warn("pending jobs deferred; provider unavailable",
			"job", job.Id,
			slog.Any("error", err),
		)
		return false
	}

	if err := p.runJob(job.Id, snap); err != nil {
		p.app.Logger().Error("job error", "job", job.Id, slog.Any("error", err))
		// Push the job's next attempt back if it is still immediately due.
		// Without this, one persistently unclaimable job sits at the head of
		// nextDueJob forever and starves every job created after it.
		p.deferErroredJob(job.Id)
	}
	return true
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

// nextDueJob returns the oldest pending job whose backoff has elapsed and that
// no other drain goroutine has already picked, or nil.
//
// It reads limit+1 candidates rather than one because the globally oldest row
// is the same row for every drain. The claim transaction in runJob would still
// let exactly one through, but the losers come back with claimed=false, loop,
// and can re-read the same id before the claim is visible -- which trips the
// no-progress guard and parks a worker until the next cron tick. Handing each
// drain a different row keeps that guard for the genuine no-progress it was
// written for. limit+1 always covers it: a job is only in the set while a
// goroutine holds a slot, and each holds one at a time.
//
// Ordering stays strict global FIFO by created. One account's 500-file import
// still delays everyone else's uploads, now N times less; round-robin over
// owners is a separate change.
func (p *Processor) nextDueJob() (*core.Record, error) {
	jobs, err := p.app.FindRecordsByFilter(
		"processing_jobs",
		"status = {:status} && (next_attempt_at = '' || next_attempt_at <= {:now})",
		"created",
		p.limit+1,
		0,
		map[string]any{"status": models.JobStatusPending, "now": nowTimestamp()},
	)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, job := range jobs {
		if _, taken := p.inflight[job.Id]; taken {
			continue
		}
		p.inflight[job.Id] = struct{}{}
		return job, nil
	}
	return nil, nil
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
	p.fanOut()
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
		// A retry re-pends this same job, so last attempt's message must not
		// outlive it; failJob writes a fresh one if this attempt fails too.
		job.Set("error", "")
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

// jobOutcome names the status a finished run left behind, for the metric's
// outcome label. Pending is the retry: the run failed a step and put itself
// back on the queue. Running is a run that bailed before it could record how
// it ended -- a save that failed -- which is an error, not a state.
func jobOutcome(status string) string {
	switch status {
	case models.JobStatusPending:
		return "retry"
	case models.JobStatusRunning, "":
		return "error"
	}
	return status
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
