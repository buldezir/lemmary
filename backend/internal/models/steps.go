package models

const (
	StepPreview          = "preview"
	StepOCR              = "ocr"
	StepDetectDuplicates = "detect_duplicates"
	StepExtractMetadata  = "extract_metadata"
	StepApplyMetadata    = "apply_metadata"
	// StepEmbed builds the retrieval vectors. It runs last because the header
	// chunk embeds the document's metadata, which apply_metadata has only just
	// written.
	StepEmbed = "embed"
)

var (
	FullPipelineSteps       = []string{StepPreview, StepOCR, StepDetectDuplicates, StepExtractMetadata, StepApplyMetadata, StepEmbed}
	ExtractionPipelineSteps = []string{StepExtractMetadata, StepApplyMetadata, StepEmbed}
	// ImportPreserveSteps runs preview/OCR/near-dup detection without AI metadata
	// overwrite, so ngx title/tags/correspondent/type survive import.
	ImportPreserveSteps = []string{StepPreview, StepOCR, StepDetectDuplicates, StepEmbed}
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
	// Soft marks a failed step the pipeline was allowed to walk past. The run
	// is still recorded as failed so the failure is visible, but the job and
	// the document carry on: an embedding the provider refused is a smaller
	// loss than a document stuck in "failed" with its text and metadata intact.
	Soft       bool   `json:"soft,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}
