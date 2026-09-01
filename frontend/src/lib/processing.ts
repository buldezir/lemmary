export type ProcessingStep =
  | 'preview'
  | 'ocr'
  | 'detect_duplicates'
  | 'extract_metadata'
  | 'apply_metadata'
  | 'embed'

export type StepRunRecord = {
  name: ProcessingStep
  status: 'pending' | 'running' | 'completed' | 'failed' | 'skipped'
  attempts: number
  provider?: string
  model?: string
  prompt_version?: string
  /**
   * A failure the pipeline was allowed to continue past. The run is still
   * 'failed' so it stays visible, but the job and the document were not.
   */
  soft?: boolean
  started_at?: string
  finished_at?: string
  error?: string
}

export type ProcessingJobRecord = {
  id: string
  document: string
  status: string
  steps: ProcessingStep[]
  step_runs?: StepRunRecord[]
  current_step?: string
  started_at: string
  finished_at: string
  created: string
  updated: string
}

export const FULL_PIPELINE_STEPS: ProcessingStep[] = [
  'preview',
  'ocr',
  'detect_duplicates',
  'extract_metadata',
  'apply_metadata',
  'embed',
]

export const EXTRACTION_PIPELINE_STEPS: ProcessingStep[] = [
  'extract_metadata',
  'apply_metadata',
  'embed',
]

export const PROCESSING_STEP_LABELS: Record<ProcessingStep, string> = {
  preview: 'Preview',
  ocr: 'OCR',
  detect_duplicates: 'Detect duplicates',
  extract_metadata: 'Extract metadata',
  apply_metadata: 'Apply metadata',
  embed: 'Build search vectors',
}

export const PROCESSING_STEP_DESCRIPTIONS: Record<ProcessingStep, string> = {
  preview: 'Regenerate the first-page preview image (PDF only)',
  ocr: 'Re-run text extraction on the original file',
  detect_duplicates: 'Compare OCR text for near-duplicates (when enabled in Settings)',
  extract_metadata: 'Re-run AI metadata extraction from OCR text',
  apply_metadata: 'Write extracted metadata onto the document',
  embed: 'Re-build the passage vectors Deep Search retrieves by meaning (when an embedding model is set)',
}

export function orderedProcessingSteps(selected: Iterable<ProcessingStep>): ProcessingStep[] {
  const chosen = new Set(selected)
  return FULL_PIPELINE_STEPS.filter((step) => chosen.has(step))
}

export function forceStepsForReprocess(steps: ProcessingStep[]): ProcessingStep[] {
  return steps.filter((step) => step !== 'apply_metadata')
}

export function defaultReprocessSteps(hasOcrText: boolean): ProcessingStep[] {
  return hasOcrText ? [...EXTRACTION_PIPELINE_STEPS] : [...FULL_PIPELINE_STEPS]
}

// Which steps a bulk requeue re-runs. 'auto' decides per document: extraction
// only when OCR text survived the failed run, the full pipeline otherwise.
export type ReprocessMode = 'auto' | 'full' | 'extraction'

export const REPROCESS_MODE_LABELS: Record<ReprocessMode, string> = {
  auto: 'Auto (per document)',
  full: 'Full pipeline',
  extraction: 'Extraction only',
}
