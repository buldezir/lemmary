import { describe, expect, it } from 'vitest'
import { applyStep, foldSteps, type ResearchStep } from './researchSteps'

describe('applyStep', () => {
  it('starts a pending line and completes it in place', () => {
    const steps: ResearchStep[] = []
    applyStep(steps, { kind: 'search', status: 'start', query: 'plumbing' })
    expect(steps).toEqual([{ kind: 'search', label: 'Searching “plumbing”', done: false }])
    applyStep(steps, { kind: 'search', status: 'done', query: 'plumbing', count: 2 })
    expect(steps).toEqual([{ kind: 'search', label: '“plumbing” — 2 documents found', done: true }])
  })
})

describe('foldSteps', () => {
  it('returns undefined for an empty trail', () => {
    expect(foldSteps(undefined)).toBeUndefined()
    expect(foldSteps([])).toBeUndefined()
  })

  it('folds a stored trail into finished labels', () => {
    expect(
      foldSteps([
        { kind: 'search', status: 'start', query: 'leak' },
        { kind: 'search', status: 'done', query: 'leak', count: 1 },
        { kind: 'answer', status: 'done' },
      ]),
    ).toEqual([
      { kind: 'search', label: '“leak” — 1 document found', done: true },
      { kind: 'answer', label: 'Answer written', done: true },
    ])
  })
})
