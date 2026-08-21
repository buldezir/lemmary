package worker

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/ocr"
)

type PipelineRunner struct {
	App      core.App
	Cfg      config.Config
	OCR      ocr.Provider
	AI       ai.Extractor
	registry map[string]Step
}

func NewPipelineRunner(app core.App, cfg config.Config, ocrProvider ocr.Provider, aiExtractor ai.Extractor) *PipelineRunner {
	return &PipelineRunner{
		App:      app,
		Cfg:      cfg,
		OCR:      ocrProvider,
		AI:       aiExtractor,
		registry: buildRegistry(ocrProvider, aiExtractor),
	}
}

func (r *PipelineRunner) Run(ctx context.Context, jobID string) error {
	jobStart := time.Now()
	logger := r.App.Logger().With("job", jobID)

	job, err := r.App.FindRecordById("processing_jobs", jobID)
	if err != nil {
		return err
	}
	if job.GetString("status") != models.JobStatusRunning {
		return nil
	}

	documentID := job.GetString("document")
	steps, err := parseSteps(job)
	if err != nil {
		return failJob(r.App, job, nil, err)
	}
	if len(steps) == 0 {
		return failJob(r.App, job, nil, fmt.Errorf("job has no steps"))
	}

	document, err := r.App.FindRecordById("documents", documentID)
	if err != nil {
		return failJob(r.App, job, nil, fmt.Errorf("load document: %w", err))
	}

	logger = logger.With("document", documentID)
	logger.Info("starting pipeline", "steps", steps)

	runs, err := parseStepRuns(job)
	if err != nil {
		return failJob(r.App, job, document, err)
	}
	runs = syncStepRuns(steps, runs)
	saveStepRuns(job, runs)

	metadata, err := loadMetadataJSON(job)
	if err != nil {
		return failJob(r.App, job, document, err)
	}

	state := &StepState{
		App:        r.App,
		Cfg:        r.Cfg,
		Job:        job,
		Document:   document,
		OCR:        r.OCR,
		AI:         r.AI,
		Metadata:   metadata,
		MimeType:   ocr.GuessMimeType(document.GetString("file")),
		ForceSteps: parseForceSteps(job),
		Logger:     logger,
	}
	defer func() {
		if state.Cleanup != nil {
			state.Cleanup()
		}
	}()

	jobCtx, jobCancel := context.WithTimeout(ctx, r.Cfg.WorkerTimeout)
	defer jobCancel()

	for {
		idx := nextRunnableIndex(runs)
		if idx < 0 {
			break
		}

		stepName := steps[idx]
		step, ok := r.registry[stepName]
		if !ok {
			return r.failStep(job, document, runs, idx, fmt.Errorf("unknown step %q", stepName))
		}

		markStepRunning(&runs[idx])
		setStepRunExecutionDetails(&runs[idx], state)
		job.Set("current_step", stepName)
		saveStepRuns(job, runs)
		if err := r.App.Save(job); err != nil {
			return err
		}

		skipped, err := step.ShouldSkip(state)
		if err != nil {
			return r.failStep(job, document, runs, idx, err)
		}
		if skipped {
			markStepCompleted(&runs[idx], true)
			saveStepRuns(job, runs)
			if err := r.App.Save(job); err != nil {
				return err
			}
			logger.Info("step skipped", "step", stepName)
			continue
		}

		logger.Info("step running", "step", stepName, "attempt", runs[idx].Attempts)

		if err := step.Run(jobCtx, state); err != nil {
			return r.handleStepFailure(job, document, runs, idx, err)
		}

		markStepCompleted(&runs[idx], false)
		saveStepRuns(job, runs)
		if err := r.App.Save(job); err != nil {
			return err
		}
		logger.Info("step completed", "step", stepName)
	}

	if document.GetString("duplicate_of") != "" {
		document.Set("processing_status", models.DocStatusNeedsReview)
		if err := r.App.Save(document); err != nil {
			return err
		}
		if job.GetString("status") == models.JobStatusRunning || job.GetString("status") == models.JobStatusCompleted {
			job.Set("status", models.JobStatusNeedsReview)
		}
	} else if job.GetString("status") == models.JobStatusRunning {
		job.Set("status", models.JobStatusCompleted)
	}
	job.Set("finished_at", nowTimestamp())
	job.Set("current_step", "")

	if err := finalizeDocumentWithoutApply(r.App, document, steps); err != nil {
		return err
	}

	logger.Info("pipeline finished",
		"status", job.GetString("status"),
		"duration", time.Since(jobStart).Round(time.Millisecond),
	)
	return r.App.Save(job)
}

func (r *PipelineRunner) failStep(job, document *core.Record, runs []models.StepRun, idx int, err error) error {
	markStepFailed(&runs[idx], err)
	saveStepRuns(job, runs)
	job.Set("current_step", runs[idx].Name)
	_ = r.App.Save(job)
	return failJob(r.App, job, document, err)
}

func (r *PipelineRunner) handleStepFailure(job, document *core.Record, runs []models.StepRun, idx int, err error) error {
	markStepFailed(&runs[idx], err)
	saveStepRuns(job, runs)
	job.Set("current_step", runs[idx].Name)

	if runs[idx].Attempts < r.Cfg.WorkerMaxRetries {
		delay := RetryDelay(runs[idx].Attempts)
		r.App.Logger().Warn("scheduling step retry",
			"job", job.Id,
			"document", document.Id,
			"step", runs[idx].Name,
			"attempt", runs[idx].Attempts,
			"max_retries", r.Cfg.WorkerMaxRetries,
			"retry_in", delay,
		)
		job.Set("status", models.JobStatusPending)
		job.Set("next_attempt_at", timestampAfter(delay))
		document.Set("processing_status", models.DocStatusPending)
		if saveErr := r.App.Save(document); saveErr != nil {
			return saveErr
		}
		return r.App.Save(job)
	}

	return failJob(r.App, job, document, err)
}

func failJob(app core.App, job *core.Record, document *core.Record, err error) error {
	documentID := ""
	if document != nil {
		documentID = document.Id
	} else {
		documentID = job.GetString("document")
	}
	app.Logger().Error("job failed",
		"job", job.Id,
		"document", documentID,
		slog.Any("error", err),
	)

	job.Set("status", models.JobStatusFailed)
	job.Set("finished_at", nowTimestamp())
	if saveErr := app.Save(job); saveErr != nil {
		return saveErr
	}

	if document != nil {
		document.Set("processing_status", models.DocStatusFailed)
		if saveErr := app.Save(document); saveErr != nil {
			return saveErr
		}
	}

	return err
}

func finalizeDocumentWithoutApply(app core.App, document *core.Record, steps []string) error {
	if document.GetString("duplicate_of") != "" {
		document.Set("processing_status", models.DocStatusNeedsReview)
		return app.Save(document)
	}
	if slices.Contains(steps, models.StepApplyMetadata) {
		// Apply step sets status when it runs; if it was skipped (e.g. duplicate),
		// leave whatever status was already set above / by earlier steps.
		if document.GetString("processing_status") == models.DocStatusProcessing {
			document.Set("processing_status", models.DocStatusCompleted)
			return app.Save(document)
		}
		return nil
	}
	document.Set("processing_status", models.DocStatusCompleted)
	return app.Save(document)
}
