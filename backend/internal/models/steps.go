package models

const (
	// StepUnlock stops a PDF that needs a user password before anything reads
	// it, so nothing reaches an OCR or AI provider.
	StepUnlock           = "unlock"
	StepPreview          = "preview"
	StepOCR              = "ocr"
	StepDetectDuplicates = "detect_duplicates"
	StepExtractMetadata  = "extract_metadata"
	StepApplyMetadata    = "apply_metadata"
	// StepRunRules runs the owner's rules after apply_metadata, so they read
	// the metadata the document ended up with, and before embed, so the index
	// carries what they changed.
	StepRunRules = "run_rules"
	// StepEmbed runs after apply_metadata so the header chunk indexes the
	// metadata the document ended up with, and after it so a document whose
	// extraction failed is still embedded.
	StepEmbed = "embed"
	// StepLinkRelated runs after embed: it reads the reference numbers apply
	// stored and the vectors embed wrote.
	StepLinkRelated = "link_related"
)

var (
	FullPipelineSteps       = []string{StepUnlock, StepPreview, StepOCR, StepDetectDuplicates, StepExtractMetadata, StepApplyMetadata, StepRunRules, StepEmbed, StepLinkRelated}
	ExtractionPipelineSteps = []string{StepExtractMetadata, StepApplyMetadata, StepRunRules, StepEmbed, StepLinkRelated}
	// ImportPreserveSteps runs preview/OCR/near-dup detection without AI metadata
	// overwrite, so ngx title/tags/correspondent/type survive import.
	ImportPreserveSteps = []string{StepUnlock, StepPreview, StepOCR, StepDetectDuplicates, StepEmbed, StepLinkRelated}
	// EditedRulesSteps runs the rules again after the owner edited a document.
	EditedRulesSteps = []string{StepRunRules, StepEmbed}
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
	Soft bool `json:"soft,omitempty"`
	// Held marks a step whose failure was refunded because processing was
	// held at the time. Only once: a second failure counts, so a document
	// that takes the server down with it still ends on failed.
	Held       bool   `json:"held,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
	// Detail is what the step reports beyond its status: run_rules names the
	// rules that matched.
	Detail string `json:"detail,omitempty"`
}
