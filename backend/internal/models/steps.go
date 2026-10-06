package models

const (
	StepPreview          = "preview"
	StepOCR              = "ocr"
	StepDetectDuplicates = "detect_duplicates"
	StepExtractMetadata  = "extract_metadata"
	StepApplyMetadata    = "apply_metadata"
	// StepEmbed runs after apply_metadata so the header chunk indexes the
	// metadata the document ended up with, and after it so a document whose
	// extraction failed is still embedded.
	StepEmbed = "embed"
	// StepLinkRelated runs after embed: it reads the reference numbers apply
	// stored and the vectors embed wrote.
	StepLinkRelated = "link_related"
)

var (
	FullPipelineSteps       = []string{StepPreview, StepOCR, StepDetectDuplicates, StepExtractMetadata, StepApplyMetadata, StepEmbed, StepLinkRelated}
	ExtractionPipelineSteps = []string{StepExtractMetadata, StepApplyMetadata, StepEmbed, StepLinkRelated}
	// ImportPreserveSteps runs preview/OCR/near-dup detection without AI metadata
	// overwrite, so ngx title/tags/correspondent/type survive import.
	ImportPreserveSteps = []string{StepPreview, StepOCR, StepDetectDuplicates, StepEmbed, StepLinkRelated}
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
	// Soft marks a failed step the pipeline was allowed to walk past: the run
	// is still recorded as failed, but the job and the document carry on.
	Soft       bool   `json:"soft,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}
