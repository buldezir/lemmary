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
	app core.App
	rt  *config.Runtime
	// How many pipelines may run at once: a document is mostly time spent
	// waiting on someone else's HTTP server.
	limit int

	mu     sync.Mutex
	active int
	// Jobs a live drain has picked but not yet claimed in the database.
	// Without it every drain selects the same oldest row and the losers of the
	// claim transaction spin on a job they can never have.
	inflight map[string]struct{}

	// rt.Snapshot in production; a field because a drain test has to hand the
	// pipeline stub providers.
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

	// A second cron on the same expression: the job drain only reaches
	// documents a job was created for, and most needing embedding never get one.
	registerEmbeddingBackfill(app, backfill)

	app.Logger().Info("worker registered", "cron", cronExpr, "concurrency", concurrency)
}

// The pipeline runs outside any transaction, so a crash mid-run strands the job
// (nextDueJob only picks pending) and its document in "processing" with no way
// back, since bulk reprocess skips processing documents.
func (p *Processor) recoverStaleRunningJobs() {
	// Unfinished and not queued, rather than status = running: a crash during
	// embed strands a job apply_metadata already marked completed, and the
	// enqueue guard keyed on finished_at would then block its document forever.
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
		// writable by the document's owner, so overrides arrive from a browser
		// as well as through the custom endpoint.
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

// The entry point for callers outside this package. forceSteps may be nil and
// overrides may be zero, which runs the job on the bindings in Settings.
func Enqueue(app core.App, documentID string, steps []string, forceSteps []string, overrides config.Overrides) (*core.Record, error) {
	return createProcessingJob(app, documentID, steps, forceSteps, overrides)
}

func createProcessingJob(app core.App, documentID string, steps []string, forceSteps []string, overrides config.Overrides) (*core.Record, error) {
	// Ensure-queued: a document with an active job must not get a second one,
	// or concurrent reprocess requests run OCR and extraction twice.
	//
	// Active is an empty finished_at, not a status test: apply_metadata marks
	// the job completed before embed runs, so for that whole window a running
	// job reads status=completed and a status test would let a second pipeline
	// in. Both terminal paths (Run and failJob) write finished_at; a retry
	// leaves it empty and re-pends, which is correct.
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

// Reserves one of the p.limit concurrent pipeline slots, or reports them busy.
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

func (p *Processor) releaseJob(jobID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inflight, jobID)
}

// One drain per slot. The record hooks already fan the upload path out; this is
// for the paths only the cron reaches (a restart with a full queue), which a
// single drain would work through serially.
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

	// Counted as in-flight work so a shutdown can wait for it: cron jobs are
	// fired and forgotten and the hooks start this with a bare `go`, and with
	// encryption at rest a job still writing when the archive is sealed loses
	// everything it had done.
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
		// that leaves it runnable is caught here rather than spinning.
		if job.Id == lastJobID {
			// The previous iteration left the job runnable, so picking it up
			// again would spin. Hand it back to the cron instead.
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

// Runs one picked job and reports whether the drain should keep going. Separate
// so the release from p.inflight is deferred over every path out, including the
// ones that fail before the pipeline starts.
func (p *Processor) attempt(job *core.Record) bool {
	defer p.releaseJob(job.Id)

	// Bindings are applied before readiness is judged: a job with an override
	// may be the one job that can run on an instance whose configured provider
	// is gone, and judging the configured snapshot would defer it forever.
	snap, err := p.effectiveSnapshot(job)
	if err != nil {
		// Overrides that no longer resolve: failed rather than deferred, since
		// no amount of waiting brings a deleted provider back. The document
		// lands on "failed", where a plain reprocess picks it up again.
		//
		// Return value dropped deliberately: failJob hands back the error it
		// was given whether or not the write succeeded, and logs it itself.
		_ = failJob(p.app, job, nil, err)
		return true
	}
	if err := providersReady(snap); err != nil {
		// Left pending so it runs once Settings are complete. Returning rather
		// than retrying inline keeps a fresh install with no provider from
		// becoming a hot loop; the cron re-enters on the next tick.
		p.app.Logger().Warn("pending jobs deferred; provider unavailable",
			"job", job.Id,
			slog.Any("error", err),
		)
		return false
	}

	if err := p.runJob(job.Id, snap); err != nil {
		p.app.Logger().Error("job error", "job", job.Id, slog.Any("error", err))
		// Without a backoff, one persistently unclaimable job sits at the head
		// of nextDueJob forever and starves every job created after it.
		p.deferErroredJob(job.Id)
	}
	return true
}

// A short backoff for a job that errored while still pending and due, so the
// drain loop can move past it to younger jobs.
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

// The oldest pending job whose backoff has elapsed and that no other drain has
// picked, or nil.
//
// It reads limit+1 candidates because the globally oldest row is the same row
// for every drain: the losers of the claim transaction would re-read that id
// before the claim is visible, trip the no-progress guard and park a worker
// until the next cron tick. limit+1 always covers it, since a job is only in
// the set while a goroutine holds a slot.
//
// Ordering stays strict global FIFO by created; round-robin over owners is a
// separate change.
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
		// A retry re-pends this same job, so the last attempt's message must
		// not outlive it.
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
