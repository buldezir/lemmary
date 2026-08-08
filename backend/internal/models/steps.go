package models

const (
	StepPreview           = "preview"
	StepOCR               = "ocr"
	StepDetectDuplicates  = "detect_duplicates"
	StepExtractMetadata   = "extract_metadata"
	StepApplyMetadata     = "apply_metadata"
)

var (
	FullPipelineSteps       = []string{StepPreview, StepOCR, StepDetectDuplicates, StepExtractMetadata, StepApplyMetadata}
	ExtractionPipelineSteps = []string{StepExtractMetadata, StepApplyMetadata}
)

const (
	StepStatusPending   = "pending"
	StepStatusRunning   = "running"
	StepStatusCompleted = "completed"
	StepStatusFailed    = "failed"
	StepStatusSkipped   = "skipped"
)

type StepRun struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	Error         string `json:"error,omitempty"`
}
