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
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/duplicates"
	"paperless-go/backend/internal/models"
)

type Processor struct {
	app        core.App
	rt         *config.Runtime
	processing sync.Mutex
}

func Register(app core.App, rt *config.Runtime) {
	p := &Processor{
		app: app,
		rt:  rt,
	}
	p.registerHooks()

	cronExpr := config.WorkerCronFromEnv()
	app.Cron().MustAdd("process_pending_jobs", cronExpr, func() {
		if err := p.processNextPending(); err != nil {
			app.Logger().Error("cron error", slog.Any("error", err))
		}
	})

	app.Logger().Info("worker registered", "cron", cronExpr)
}

func (p *Processor) registerHooks() {
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

		steps := takeCreateStepsForChecksum(record.GetString("checksum"))
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

	p.app.OnRecordDelete("documents").BindFunc(func(e *core.RecordEvent) error {
		jobs, err := e.App.FindRecordsByFilter(
			"processing_jobs",
			"document = {:docId}",
			"-created",
			100,
			0,
			map[string]any{"docId": e.Record.Id},
		)
		if err != nil {
			return err
		}

		for _, job := range jobs {
			if err := e.App.Delete(job); err != nil {
				return err
			}
		}

		return e.Next()
	})

	p.app.OnRecordAfterCreateSuccess("processing_jobs").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("status") == models.JobStatusPending {
			go p.dispatch(e.Record.Id)
		}
		return e.Next()
	})

	p.app.OnRecordAfterUpdateSuccess("processing_jobs").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("status") == models.JobStatusPending {
			go p.dispatch(e.Record.Id)
		}
		return e.Next()
	})
}

func createProcessingJob(app core.App, documentID string, steps []string, forceSteps []string) (*core.Record, error) {
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

func (p *Processor) dispatch(jobID string) {
	_ = jobID
	go p.drainPending()
}

func (p *Processor) drainPending() {
	if !p.processing.TryLock() {
		return
	}
	defer p.processing.Unlock()

	for {
		jobs, err := p.app.FindRecordsByFilter(
			"processing_jobs",
			"status = {:status}",
			"created",
			1,
			0,
			map[string]any{"status": models.JobStatusPending},
		)
		if err != nil {
			p.app.Logger().Error("list pending jobs", slog.Any("error", err))
			return
		}
		if len(jobs) == 0 {
			return
		}
		if err := p.runJob(jobs[0].Id); err != nil {
			p.app.Logger().Error("job error", "job", jobs[0].Id, slog.Any("error", err))
		}
	}
}

func (p *Processor) processNextPending() error {
	p.drainPending()
	return nil
}

func (p *Processor) runJob(jobID string) error {
	snap := p.rt.Snapshot()
	if snap.OCR == nil {
		return fmt.Errorf("OCR provider is not configured; update Settings")
	}
	if snap.AI == nil {
		return fmt.Errorf("AI extractor is not configured; update Settings")
	}

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
		if len(runs) == 0 {
			runs = initStepRuns(steps)
		}
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

	runner := NewPipelineRunner(p.app, snap.Cfg, snap.OCR, snap.AI)
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
