/**
 * A "period" is a year ("2025") or a month ("2025-03"). Selecting one writes the
 * existing From/To filters, so there is only ever one date filter in play.
 */

export type TimelineMonth = {
  /** "YYYY-MM". */
  month: string
  count: number
}

export type TimelineYear = {
  /** "YYYY". */
  year: string
  /** Documents across every month of this year. */
  count: number
  months: TimelineMonth[]
}

export type DateRange = {
  from: string
  to: string
}

const monthNames = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
]

/**
 * The one row that is not a date range. It travels the same onSelect path as a
 * year or a month, and the page turns it into the `undated` filter.
 */
export const UNDATED_PERIOD = 'undated'

const yearPattern = /^\d{4}$/
const monthPattern = /^\d{4}-(0[1-9]|1[0-2])$/

export function isYearPeriod(period: string): boolean {
  return yearPattern.test(period)
}

export function isMonthPeriod(period: string): boolean {
  return monthPattern.test(period)
}

/** "2025-03" -> "March". */
export function monthLabel(month: string): string {
  if (!isMonthPeriod(month)) return month
  return monthNames[Number(month.slice(5, 7)) - 1]
}

/**
 * Last day of a month, via UTC so a browser east or west of the server never
 * lands on the neighbouring month.
 */
function lastDayOfMonth(year: number, month: number): number {
  return new Date(Date.UTC(year, month, 0)).getUTCDate()
}

export function yearRange(year: string): DateRange {
  return { from: `${year}-01-01`, to: `${year}-12-31` }
}

export function monthRange(month: string): DateRange {
  const year = Number(month.slice(0, 4))
  const index = Number(month.slice(5, 7))
  const day = String(lastDayOfMonth(year, index)).padStart(2, '0')
  return { from: `${month}-01`, to: `${month}-${day}` }
}

/** The From/To range a period selects; an empty range clears the filter. */
export function periodRange(period: string | null): DateRange {
  if (period === null) return { from: '', to: '' }
  if (isYearPeriod(period)) return yearRange(period)
  if (isMonthPeriod(period)) return monthRange(period)
  return { from: '', to: '' }
}

/**
 * Derived rather than stored, so typing the dates by hand lights up the
 * matching row too.
 */
export function activePeriod(dateFrom: string, dateTo: string): string | null {
  if (!dateFrom || !dateTo) return null

  const year = dateFrom.slice(0, 4)
  if (isYearPeriod(year)) {
    const range = yearRange(year)
    if (range.from === dateFrom && range.to === dateTo) return year
  }

  const month = dateFrom.slice(0, 7)
  if (isMonthPeriod(month)) {
    const range = monthRange(month)
    if (range.from === dateFrom && range.to === dateTo) return month
  }

  return null
}

/** Newest first, keeping only the months that hold documents. */
export function groupByYear(months: TimelineMonth[]): TimelineYear[] {
  const years = new Map<string, TimelineYear>()

  for (const entry of months) {
    if (!isMonthPeriod(entry.month) || entry.count <= 0) continue
    const year = entry.month.slice(0, 4)
    const existing = years.get(year)
    if (existing) {
      existing.count += entry.count
      existing.months.push(entry)
    } else {
      years.set(year, { year, count: entry.count, months: [entry] })
    }
  }

  const grouped = [...years.values()]
  grouped.sort((a, b) => b.year.localeCompare(a.year))
  for (const year of grouped) {
    year.months.sort((a, b) => b.month.localeCompare(a.month))
  }
  return grouped
}

/**
 * The active period's year, else the year the From filter falls in, else the
 * newest. dateFrom covers a range activePeriod cannot name: 2019-01-01..06-30
 * is neither a whole year nor a whole month, and the newest year would open
 * instead, collapsing the months the filter was typed to see.
 */
export function openYear(
  active: string | null,
  years: TimelineYear[],
  dateFrom = '',
): string | null {
  // "undated" is not a period in time, so it opens nothing of its own.
  const named = active && active !== UNDATED_PERIOD ? active : dateFrom
  if (named) {
    const year = named.slice(0, 4)
    if (years.some((entry) => entry.year === year)) return year
  }
  // groupByYear already sorted newest first.
  return years[0]?.year ?? null
}

/**
 * Month rows shown at once before folding earns its keep: two years' worth
 * still fits a sidebar, and an archive that short reads better whole.
 */
const MAX_UNFOLDED_MONTHS = 24

/** Whether the timeline is long enough that only one year should show months. */
export function shouldFold(years: TimelineYear[]): boolean {
  return years.reduce((rows, year) => rows + year.months.length, 0) > MAX_UNFOLDED_MONTHS
}
