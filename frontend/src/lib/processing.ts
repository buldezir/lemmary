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

/**
 * Parse a PocketBase timestamp, which the worker writes as
 * `2006-01-02 15:04:05.000Z` -- a space where ISO 8601 wants a `T`. Date's
 * handling of that spelling is implementation-defined, so normalise it rather
 * than trust the engine. Returns null for the empty string, which is what a
 * step that has not reached a given moment carries.
 */
export function parseStepTimestamp(value?: string): number | null {
  if (!value) return null
  const ms = Date.parse(value.replace(' ', 'T'))
  return Number.isNaN(ms) ? null : ms
}

/**
 * How long a step ran, in milliseconds, or null when that is not a question
 * with an answer.
 *
 * Null covers two cases that must not render as a number. A pending step has
 * no start. A *skipped* one has only a finish: the pipeline decides to skip
 * before it books an attempt, so it never calls markStepRunning and started_at
 * stays empty -- subtracting it would print the whole Unix epoch as a duration.
 *
 * A running step is measured against `now`, so the caller re-rendering on the
 * document page's one-second poll is what makes it tick. Clamped at zero
 * because `now` is the viewer's clock and started_at is the server's, and a
 * viewer running a few seconds behind should see 0s rather than a negative.
 *
 * On a retry markStepRunning resets started_at, so this is the duration of the
 * latest attempt, not of all of them summed.
 */
export function stepDurationMs(run: StepRunRecord, now: number = Date.now()): number | null {
  const started = parseStepTimestamp(run.started_at)
  if (started === null) return null
  const finished = parseStepTimestamp(run.finished_at)
  if (finished !== null) return Math.max(0, finished - started)
  if (run.status !== 'running') return null
  return Math.max(0, now - started)
}

/** Total wall-clock for the job, on the same rules as a single step. */
export function jobDurationMs(job: ProcessingJobRecord, now: number = Date.now()): number | null {
  const started = parseStepTimestamp(job.started_at)
  if (started === null) return null
  const finished = parseStepTimestamp(job.finished_at)
  if (finished !== null) return Math.max(0, finished - started)
  return Math.max(0, now - started)
}

/**
 * A duration at one significant moment rather than a fixed unit: OCR on a
 * sidecar is seconds, applying metadata is milliseconds, and a backfilled
 * embed can be minutes. Sub-second is rounded to the millisecond because
 * "0.0s" tells a reader nothing about a step that did almost no work.
 */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(1)}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${String(Math.floor(seconds % 60)).padStart(2, '0')}s`
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, '0')}m`
}
