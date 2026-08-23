import { describe, expect, it } from 'vitest'
import {
  defaultReprocessSteps,
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
    expect(EXTRACTION_PIPELINE_STEPS).toEqual(['extract_metadata', 'apply_metadata'])
  })
})
