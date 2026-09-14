import { describe, expect, it } from 'vitest'
import {
  UNDATED_PERIOD,
  activePeriod,
  groupByYear,
  monthLabel,
  monthRange,
  openYear,
  shouldFold,
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

describe('openYear', () => {
  const years = groupByYear([
    { month: '2025-03', count: 1 },
    { month: '2024-07', count: 2 },
    { month: '2023-01', count: 3 },
  ])

  it('opens the newest year when nothing is filtered', () => {
    expect(openYear(null, years)).toBe('2025')
  })

  it('opens the year holding the selected month', () => {
    expect(openYear('2024-07', years)).toBe('2024')
  })

  it('opens a selected year', () => {
    expect(openYear('2023', years)).toBe('2023')
  })

  it('falls back to the newest year for the undated row', () => {
    expect(openYear(UNDATED_PERIOD, years)).toBe('2025')
  })

  it('falls back to the newest year when the period names a year with no documents', () => {
    expect(openYear('2019-05', years)).toBe('2025')
  })

  it('opens nothing when there are no years', () => {
    expect(openYear('2025-03', [])).toBeNull()
  })

  // A part-year range is no period at all, so the From date names the year;
  // otherwise the newest year opens and hides the months just typed.
  it('opens the year a part-year range starts in', () => {
    expect(openYear(null, years, '2024-02-10')).toBe('2024')
  })

  it('prefers the selected period over the From date', () => {
    expect(openYear('2023', years, '2024-02-10')).toBe('2023')
  })

  it('ignores a From date in a year with no documents', () => {
    expect(openYear(null, years, '2019-06-30')).toBe('2025')
  })

  it('opens the newest year for the undated row whatever the From date says', () => {
    expect(openYear(UNDATED_PERIOD, years, '')).toBe('2025')
  })
})

describe('shouldFold', () => {
  function months(count: number, from = 2025): { month: string; count: number }[] {
    return Array.from({ length: count }, (_, index) => ({
      month: `${from - Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`,
      count: 1,
    }))
  }

  it('leaves a short archive whole', () => {
    expect(shouldFold(groupByYear(months(24)))).toBe(false)
  })

  it('folds once the months outgrow the column', () => {
    expect(shouldFold(groupByYear(months(25)))).toBe(true)
  })

  it('counts months, not years -- three sparse years still fit', () => {
    expect(
      shouldFold(
        groupByYear([
          { month: '2025-03', count: 1 },
          { month: '2024-07', count: 2 },
          { month: '2023-01', count: 3 },
        ]),
      ),
    ).toBe(false)
  })

  it('has nothing to fold for an empty library', () => {
    expect(shouldFold([])).toBe(false)
  })
})
