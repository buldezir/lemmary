import { describe, expect, it } from 'vitest'
import {
  activePeriod,
  groupByYear,
  monthLabel,
  monthRange,
  periodRange,
  yearRange,
} from './timeline'

describe('monthRange', () => {
  it('spans the whole month', () => {
    expect(monthRange('2025-03')).toEqual({ from: '2025-03-01', to: '2025-03-31' })
    expect(monthRange('2025-04')).toEqual({ from: '2025-04-01', to: '2025-04-30' })
  })

  it('gets February right in both common and leap years', () => {
    expect(monthRange('2025-02')).toEqual({ from: '2025-02-01', to: '2025-02-28' })
    expect(monthRange('2024-02')).toEqual({ from: '2024-02-01', to: '2024-02-29' })
    expect(monthRange('1900-02')).toEqual({ from: '1900-02-01', to: '1900-02-28' })
    expect(monthRange('2000-02')).toEqual({ from: '2000-02-01', to: '2000-02-29' })
  })

  it('spans December without rolling into the next year', () => {
    expect(monthRange('2025-12')).toEqual({ from: '2025-12-01', to: '2025-12-31' })
  })
})

describe('yearRange', () => {
  it('spans the whole year', () => {
    expect(yearRange('2025')).toEqual({ from: '2025-01-01', to: '2025-12-31' })
  })
})

describe('periodRange', () => {
  it('clears the filter for no selection', () => {
    expect(periodRange(null)).toEqual({ from: '', to: '' })
  })

  it('picks the range by the shape of the period', () => {
    expect(periodRange('2025')).toEqual({ from: '2025-01-01', to: '2025-12-31' })
    expect(periodRange('2025-07')).toEqual({ from: '2025-07-01', to: '2025-07-31' })
  })

  it('clears the filter rather than guessing at a malformed period', () => {
    expect(periodRange('2025-13')).toEqual({ from: '', to: '' })
    expect(periodRange('nope')).toEqual({ from: '', to: '' })
  })
})

describe('activePeriod', () => {
  it('recognises a month and a year selection', () => {
    expect(activePeriod('2025-03-01', '2025-03-31')).toBe('2025-03')
    expect(activePeriod('2025-01-01', '2025-12-31')).toBe('2025')
  })

  it('is null for a hand-typed range that is not a whole period', () => {
    expect(activePeriod('2025-03-02', '2025-03-31')).toBeNull()
    expect(activePeriod('2025-01-01', '2025-06-30')).toBeNull()
  })

  it('is null when only one end of the range is set', () => {
    expect(activePeriod('2025-03-01', '')).toBeNull()
    expect(activePeriod('', '2025-03-31')).toBeNull()
    expect(activePeriod('', '')).toBeNull()
  })
})

describe('monthLabel', () => {
  it('names the month', () => {
    expect(monthLabel('2025-01')).toBe('January')
    expect(monthLabel('2025-12')).toBe('December')
  })

  it('passes a malformed month through unchanged', () => {
    expect(monthLabel('2025-13')).toBe('2025-13')
  })
})

describe('groupByYear', () => {
  it('groups months under their year, newest first, with year totals', () => {
    expect(
      groupByYear([
        { month: '2024-01', count: 2 },
        { month: '2025-10', count: 4 },
        { month: '2025-03', count: 1 },
      ]),
    ).toEqual([
      {
        year: '2025',
        count: 5,
        months: [
          { month: '2025-10', count: 4 },
          { month: '2025-03', count: 1 },
        ],
      },
      { year: '2024', count: 2, months: [{ month: '2024-01', count: 2 }] },
    ])
  })

  it('drops empty and malformed buckets', () => {
    expect(
      groupByYear([
        { month: '', count: 3 },
        { month: '2025-00', count: 1 },
        { month: '2025-03', count: 0 },
        { month: '2025-04', count: 1 },
      ]),
    ).toEqual([{ year: '2025', count: 1, months: [{ month: '2025-04', count: 1 }] }])
  })

  it('has nothing to group for an empty library', () => {
    expect(groupByYear([])).toEqual([])
  })
})
