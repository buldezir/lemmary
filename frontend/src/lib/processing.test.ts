import { describe, expect, it } from 'vitest'
import {
  defaultReprocessSteps,
  formatDuration,
  jobDurationMs,
  jobStillRunning,
  parseStepTimestamp,
  stalledAfterMs,
  stepDurationMs,
  summarizeJob,
  type ProcessingJobRecord,
  type StepRunRecord,
  EXTRACTION_PIPELINE_STEPS,
  forceStepsForReprocess,
  FULL_PIPELINE_STEPS,
  orderedProcessingSteps,
} from './processing'

describe('orderedProcessingSteps', () => {
  it('returns steps in pipeline order regardless of selection order', () => {
    expect(orderedProcessingSteps(['apply_metadata', 'ocr', 'preview'])).toEqual([
      'preview',
      'ocr',
      'apply_metadata',
    ])
  })

  it('drops duplicates', () => {
    expect(orderedProcessingSteps(['ocr', 'ocr'])).toEqual(['ocr'])
  })

  it('returns an empty list for an empty selection', () => {
    expect(orderedProcessingSteps([])).toEqual([])
  })
})

describe('forceStepsForReprocess', () => {
  it('excludes apply_metadata, which is never forced', () => {
    expect(forceStepsForReprocess([...FULL_PIPELINE_STEPS])).toEqual([
      'preview',
      'ocr',
      'detect_duplicates',
      'extract_metadata',
      'embed',
    ])
  })
})

describe('defaultReprocessSteps', () => {
  it('defaults to extraction only when OCR text already exists', () => {
    expect(defaultReprocessSteps(true)).toEqual(EXTRACTION_PIPELINE_STEPS)
  })

  it('defaults to the full pipeline without OCR text', () => {
    expect(defaultReprocessSteps(false)).toEqual(FULL_PIPELINE_STEPS)
  })

  it('returns fresh arrays that callers may mutate', () => {
    const steps = defaultReprocessSteps(true)
    steps.push('preview')
    expect(EXTRACTION_PIPELINE_STEPS).toEqual(['extract_metadata', 'apply_metadata', 'embed'])
  })
})


// The worker writes `2006-01-02 15:04:05.000Z` -- a space where ISO 8601 wants
// a T, which Date parses at the engine's discretion.
describe('parseStepTimestamp', () => {
  it('reads the space-separated spelling the worker writes', () => {
    expect(parseStepTimestamp('2026-09-04 14:16:45.351Z')).toBe(Date.UTC(2026, 8, 4, 14, 16, 45, 351))
  })

  it('treats the empty string and nonsense as absent, not as the epoch', () => {
    expect(parseStepTimestamp('')).toBeNull()
    expect(parseStepTimestamp(undefined)).toBeNull()
    expect(parseStepTimestamp('not a date')).toBeNull()
  })
})

function run(over: Partial<StepRunRecord> = {}): StepRunRecord {
  return { name: 'ocr', status: 'completed', attempts: 1, ...over }
}

describe('stepDurationMs', () => {
  it('measures a finished step between its two timestamps', () => {
    const ms = stepDurationMs(
      run({ started_at: '2026-09-04 14:16:45.000Z', finished_at: '2026-09-04 14:16:49.200Z' }),
    )
    expect(ms).toBe(4200)
  })

  // The trap this guards: the pipeline decides to skip before it books an
  // attempt, so a skipped run has finished_at and no started_at. Subtracting
  // would print 56 years.
  it('reports nothing for a skipped step, which finishes without starting', () => {
    expect(stepDurationMs(run({ status: 'skipped', finished_at: '2026-09-04 14:16:49.200Z' }))).toBeNull()
  })

  it('reports nothing for a step that has not started', () => {
    expect(stepDurationMs(run({ status: 'pending' }))).toBeNull()
  })

  it('measures a running step against now, so it ticks', () => {
    const started = Date.UTC(2026, 8, 4, 14, 16, 45, 0)
    expect(stepDurationMs(run({ status: 'running', started_at: '2026-09-04 14:16:45.000Z' }), started + 3000)).toBe(3000)
  })

  // started_at is the server's clock and `now` is the viewer's; a viewer a few
  // seconds behind should read 0s, never a negative duration.
  it('clamps at zero when the viewer clock is behind the server', () => {
    const started = Date.UTC(2026, 8, 4, 14, 16, 45, 0)
    expect(stepDurationMs(run({ status: 'running', started_at: '2026-09-04 14:16:45.000Z' }), started - 9000)).toBe(0)
  })

  // A step that died without writing finished_at must not read as still running.
  it('reports nothing for a non-running step with no finish', () => {
    expect(stepDurationMs(run({ status: 'failed', started_at: '2026-09-04 14:16:45.000Z' }))).toBeNull()
  })
})

describe('jobDurationMs', () => {
  const job = (over: Partial<ProcessingJobRecord>): ProcessingJobRecord => ({
    id: 'j1', document: 'd1', status: 'completed', steps: [],
    started_at: '', finished_at: '', created: '', updated: '', ...over,
  })

  it('measures a finished job end to end', () => {
    expect(jobDurationMs(job({ started_at: '2026-09-04 14:16:45.000Z', finished_at: '2026-09-04 14:17:15.000Z' }))).toBe(30000)
  })

  it('measures an unfinished job against now', () => {
    const started = Date.UTC(2026, 8, 4, 14, 16, 45, 0)
    expect(jobDurationMs(job({ started_at: '2026-09-04 14:16:45.000Z' }), started + 5000)).toBe(5000)
  })

  it('reports nothing for a job that never started', () => {
    expect(jobDurationMs(job({}))).toBeNull()
  })
})

describe('formatDuration', () => {
  it('keeps sub-second work in milliseconds, where "0.0s" would say nothing', () => {
    expect(formatDuration(0)).toBe('0ms')
    expect(formatDuration(12)).toBe('12ms')
    expect(formatDuration(999)).toBe('999ms')
  })

  it('shows seconds with one decimal', () => {
    expect(formatDuration(1000)).toBe('1.0s')
    expect(formatDuration(4234)).toBe('4.2s')
    expect(formatDuration(59900)).toBe('59.9s')
  })

  it('shows minutes and padded seconds', () => {
    expect(formatDuration(60000)).toBe('1m 00s')
    expect(formatDuration(185000)).toBe('3m 05s')
  })

  it('shows hours and padded minutes for a long backfill', () => {
    expect(formatDuration(3600000)).toBe('1h 00m')
    expect(formatDuration(3840000)).toBe('1h 04m')
  })
})


// The bug: the document page stopped polling on the document's own status, and
// apply_metadata marks the document completed *before* embed runs. The panel
// froze mid-pipeline with embed reading 'running', and its duration counted up
// for ever because nothing was left to fetch the finish.
describe('jobStillRunning', () => {
  const job = (over: Partial<ProcessingJobRecord>): ProcessingJobRecord => ({
    id: 'j1', document: 'd1', status: 'completed', steps: [],
    started_at: '', finished_at: '', created: '', updated: '', ...over,
  })

  it('is true while embed runs, though the job status already says completed', () => {
    expect(
      jobStillRunning(job({ status: 'completed', started_at: '2026-09-04 15:11:35.176Z' })),
    ).toBe(true)
  })

  it('is false once finished_at is written, which happens once at the very end', () => {
    expect(
      jobStillRunning(
        job({ started_at: '2026-09-04 15:11:35.176Z', finished_at: '2026-09-04 15:11:50.117Z' }),
      ),
    ).toBe(false)
  })

  it('is false for a job that never started, and for no job at all', () => {
    expect(jobStillRunning(job({}))).toBe(false)
    expect(jobStillRunning(null)).toBe(false)
    expect(jobStillRunning(undefined)).toBe(false)
  })
})

describe('summarizeJob', () => {
  const now = Date.UTC(2026, 8, 4, 14, 17, 0, 0)
  const at = (offsetMs: number) => new Date(now + offsetMs).toISOString().replace('T', ' ')

  const job = (over: Partial<ProcessingJobRecord>): ProcessingJobRecord => ({
    id: 'j1', document: 'd1', status: 'running', steps: [],
    started_at: at(-30_000), finished_at: '', created: at(-30_000), updated: '', ...over,
  })

  it('names the running step and how long it has been going', () => {
    const summary = summarizeJob(
      job({ step_runs: [run({ status: 'running', started_at: at(-12_000) })] }),
      now,
    )
    expect(summary).toEqual({ tone: 'running', label: 'OCR — 12.0s' })
  })

  // The window handleStepFailure opens: the run is already marked failed, but
  // the job has been re-pended for another go. Reading that as "failed" would
  // show an error for work that is still being attempted.
  it('reads a failed step on a re-pended job as a retry, not a failure', () => {
    expect(
      summarizeJob(
        job({
          status: 'pending',
          step_runs: [run({ status: 'failed', attempts: 1, error: 'mistral: 429' })],
        }),
        now,
      ),
    ).toEqual({ tone: 'running', label: 'Retrying OCR (attempt 2)', detail: 'mistral: 429' })
  })

  it('reports a failed step with the provider message', () => {
    expect(
      summarizeJob(
        job({
          status: 'failed',
          finished_at: at(-1000),
          step_runs: [run({ name: 'extract_metadata', status: 'failed', error: 'context length exceeded' })],
        }),
        now,
      ),
    ).toEqual({
      tone: 'error',
      label: 'Extract metadata failed',
      detail: 'context length exceeded',
    })
  })

  // failJob used to write job.error for a step failure too, and the summary
  // preferred it -- so every real failure read "Processing failed" instead of
  // naming the step. Old rows still carry both; the step wins.
  it('names the step even when the job also carries an error', () => {
    expect(
      summarizeJob(
        job({
          status: 'failed',
          finished_at: at(-1000),
          error: 'ocr: mistral: 429',
          step_runs: [run({ status: 'failed', error: 'mistral: 429' })],
        }),
        now,
      ),
    ).toEqual({ tone: 'error', label: 'OCR failed', detail: 'mistral: 429' })
  })

  it('borrows the job error as the detail when the step recorded none', () => {
    expect(
      summarizeJob(
        job({
          status: 'failed',
          finished_at: at(-1000),
          error: 'worker timed out',
          step_runs: [run({ status: 'failed' })],
        }),
        now,
      ),
    ).toEqual({ tone: 'error', label: 'OCR failed', detail: 'worker timed out' })
  })

  // The class of failure that leaves step_runs empty: an unparseable step list,
  // a document that would not load. Without job.error there is nothing to say.
  it('falls back to the job-level error when no step recorded one', () => {
    expect(
      summarizeJob(job({ status: 'failed', finished_at: at(-1000), error: 'job has no steps' }), now),
    ).toEqual({ tone: 'error', label: 'Processing failed', detail: 'job has no steps' })
  })

  it('says nothing about a job queued a moment ago', () => {
    expect(summarizeJob(job({ status: 'pending', started_at: '', created: at(-5_000) }), now)).toBeNull()
  })

  it('suggests a cause once a pending job has waited past the threshold', () => {
    const summary = summarizeJob(
      job({ status: 'pending', started_at: '', created: at(-stalledAfterMs - 1000) }),
      now,
    )
    expect(summary?.tone).toBe('warning')
    expect(summary?.label).toBe('Not started yet')
  })

  // The silent one: apply_metadata already wrote "completed" onto the document,
  // and only the soft-failed run says the search vectors are missing.
  it('warns about a soft failure on a job that otherwise completed', () => {
    expect(
      summarizeJob(
        job({
          status: 'completed',
          finished_at: at(-1000),
          step_runs: [
            run({ name: 'ocr', status: 'completed' }),
            run({ name: 'embed', status: 'failed', soft: true, error: 'embeddings: 503' }),
          ],
        }),
        now,
      ),
    ).toEqual({ tone: 'warning', label: 'Build search vectors failed', detail: 'embeddings: 503' })
  })

  it('covers a claimed job that is between steps', () => {
    expect(summarizeJob(job({ step_runs: [run({ status: 'completed' })] }), now)).toEqual({
      tone: 'running',
      label: 'Processing — 30.0s',
    })
  })

  it('says nothing for a clean finished job, or no job at all', () => {
    expect(
      summarizeJob(job({ status: 'completed', finished_at: at(-1000), step_runs: [run()] }), now),
    ).toBeNull()
    expect(summarizeJob(null, now)).toBeNull()
  })
})
