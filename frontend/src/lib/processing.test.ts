import { describe, expect, it } from 'vitest'
import {
  defaultReprocessSteps,
  formatDuration,
  jobDurationMs,
  parseStepTimestamp,
  stepDurationMs,
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
