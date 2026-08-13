package worker

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/models"
)

func parseStepRuns(job *core.Record) ([]models.StepRun, error) {
	raw := job.Get("step_runs")
	if raw == nil {
		return nil, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal step_runs: %w", err)
	}

	var runs []models.StepRun
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("unmarshal step_runs: %w", err)
	}
	return runs, nil
}

func initStepRuns(steps []string) []models.StepRun {
	runs := make([]models.StepRun, len(steps))
	for i, name := range steps {
		runs[i] = models.StepRun{
			Name:     name,
			Status:   models.StepStatusPending,
			Attempts: 0,
		}
	}
	return runs
}

func syncStepRuns(steps []string, existing []models.StepRun) []models.StepRun {
	byName := make(map[string]models.StepRun, len(existing))
	for _, run := range existing {
		byName[run.Name] = run
	}

	runs := make([]models.StepRun, len(steps))
	for i, name := range steps {
		if run, ok := byName[name]; ok {
			runs[i] = run
			continue
		}
		runs[i] = models.StepRun{
			Name:     name,
			Status:   models.StepStatusPending,
			Attempts: 0,
		}
	}
	return runs
}

func saveStepRuns(job *core.Record, runs []models.StepRun) {
	job.Set("step_runs", runs)
}

func nextRunnableIndex(runs []models.StepRun) int {
	for i, run := range runs {
		switch run.Status {
		case models.StepStatusCompleted, models.StepStatusSkipped:
			continue
		default:
			return i
		}
	}
	return -1
}

func markStepRunning(run *models.StepRun) {
	now := nowTimestamp()
	run.Status = models.StepStatusRunning
	run.Attempts++
	run.StartedAt = now
	run.FinishedAt = ""
	run.Error = ""
}

func setStepRunExecutionDetails(run *models.StepRun, state *StepState) {
	switch run.Name {
	case models.StepOCR:
		if state.OCR != nil {
			run.Provider = state.OCR.Name()
		}
		run.Model = state.Cfg.OCRModel
	case models.StepExtractMetadata:
		if state.AI != nil {
			run.Provider = state.AI.Name()
			run.Model = state.AI.Model()
		}
		run.PromptVersion = state.Cfg.ExtractionPromptVer
	}
}

func markStepCompleted(run *models.StepRun, skipped bool) {
	now := nowTimestamp()
	if skipped {
		run.Status = models.StepStatusSkipped
	} else {
		run.Status = models.StepStatusCompleted
	}
	run.FinishedAt = now
	run.Error = ""
}

func markStepFailed(run *models.StepRun, err error) {
	now := nowTimestamp()
	run.Status = models.StepStatusFailed
	run.FinishedAt = now
	run.Error = truncateError(err.Error(), 1900)
}
