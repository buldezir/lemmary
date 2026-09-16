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

describe('web steps', () => {
  it('labels a web search by its query and its result count', () => {
    const steps: ResearchStep[] = []
    applyStep(steps, { kind: 'web_search', status: 'start', query: 'vat rate 2026' })
    expect(steps).toEqual([
      { kind: 'web_search', label: 'Searching the web for “vat rate 2026”', done: false },
    ])
    applyStep(steps, { kind: 'web_search', status: 'done', query: 'vat rate 2026', count: 5 })
    expect(steps).toEqual([
      { kind: 'web_search', label: '“vat rate 2026” — 5 results', done: true },
    ])
  })

  it('falls back to a plain label when a web search carried no query', () => {
    const steps: ResearchStep[] = []
    applyStep(steps, { kind: 'web_search', status: 'start' })
    expect(steps[0].label).toBe('Searching the web')
    applyStep(steps, { kind: 'web_search', status: 'done', count: 1 })
    expect(steps[0].label).toBe('Searched the web — 1 result')
  })

  // Hostnames, not URLs: a URL is too long for a step line, and the page's own
  // title is not known until it has been read.
  it('labels a fetch by the hosts it read', () => {
    const steps: ResearchStep[] = []
    applyStep(steps, { kind: 'web_fetch', status: 'start', count: 2 })
    expect(steps[0].label).toBe('Reading 2 pages')
    applyStep(steps, {
      kind: 'web_fetch',
      status: 'done',
      titles: ['example.com', 'example.org'],
      count: 2,
    })
    expect(steps[0]).toEqual({ kind: 'web_fetch', label: 'Read example.com, example.org', done: true })
  })

  it('summarises more than three hosts', () => {
    const steps: ResearchStep[] = []
    applyStep(steps, { kind: 'web_fetch', status: 'start', count: 5 })
    applyStep(steps, {
      kind: 'web_fetch',
      status: 'done',
      titles: ['a.example', 'b.example', 'c.example', 'd.example', 'e.example'],
      count: 5,
    })
    expect(steps[0].label).toBe('Read a.example, b.example, c.example, and 2 more')
  })

  // A fetch that read nothing must not erase the line that said it was trying.
  it('keeps the pending label when a fetch came back with no hosts', () => {
    const steps: ResearchStep[] = []
    applyStep(steps, { kind: 'web_fetch', status: 'start', count: 1 })
    applyStep(steps, { kind: 'web_fetch', status: 'done' })
    expect(steps[0]).toEqual({ kind: 'web_fetch', label: 'Reading 1 page', done: true })
  })

  it('folds a stored web trail', () => {
    expect(
      foldSteps([
        { kind: 'web_search', status: 'start', query: 'leak' },
        { kind: 'web_search', status: 'done', query: 'leak', count: 2 },
        { kind: 'web_fetch', status: 'start', count: 1 },
        { kind: 'web_fetch', status: 'done', titles: ['example.com'], count: 1 },
      ]),
    ).toEqual([
      { kind: 'web_search', label: '“leak” — 2 results', done: true },
      { kind: 'web_fetch', label: 'Read example.com', done: true },
    ])
  })
})
