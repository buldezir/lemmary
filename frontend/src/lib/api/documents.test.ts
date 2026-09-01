import { describe, expect, it } from 'vitest'
import { buildDocumentFilter, parseDuplicateOfId } from './documents'

const noFilters = {
  status: 'all',
  documentType: 'all',
  correspondent: 'all',
  dateFrom: '',
  dateTo: '',
}

describe('buildDocumentFilter', () => {
  it('returns undefined when nothing is filtered', () => {
    expect(buildDocumentFilter(noFilters)).toBeUndefined()
  })

  it('combines active filters with &&', () => {
    expect(
      buildDocumentFilter({
        ...noFilters,
        status: 'failed',
        dateFrom: '2026-01-01',
        dateTo: '2026-02-01',
      }),
    ).toBe(
      "processing_status = 'failed' && document_date >= '2026-01-01' && document_date < '2026-02-02'",
    )
  })

  it('bounds dateTo exclusively on the next day so the To day is included', () => {
    // document_date is a DateField: stored as "YYYY-MM-DD HH:MM:SS.sssZ" and
    // compared as a string. `<= '2025-03-31'` sorts before every timestamp on
    // the 31st, so an inclusive-looking To silently dropped that whole day --
    // exactly the day a timeline month click selects.
    const stored = '2025-03-31 00:00:00.000Z'
    const filter = buildDocumentFilter({ ...noFilters, dateTo: '2025-03-31' })
    expect(filter).toBe("document_date < '2025-04-01'")
    expect(stored <= '2025-03-31').toBe(false)
    expect(stored < '2025-04-01').toBe(true)
  })

  it('rolls dateTo over month and year ends, and across a leap day', () => {
    const to = (dateTo: string) => buildDocumentFilter({ ...noFilters, dateTo })
    expect(to('2025-12-31')).toBe("document_date < '2026-01-01'")
    expect(to('2024-02-29')).toBe("document_date < '2024-03-01'")
    expect(to('2025-02-28')).toBe("document_date < '2025-03-01'")
  })

  it('passes an unparseable dateTo through rather than inventing a bound', () => {
    expect(buildDocumentFilter({ ...noFilters, dateTo: 'garbage' })).toBe(
      "document_date < 'garbage'",
    )
  })

  it('filters by taxonomy record ids', () => {
    expect(
      buildDocumentFilter({ ...noFilters, documentType: 'type123', correspondent: 'corr456' }),
    ).toBe("document_type = 'type123' && correspondent = 'corr456'")
  })

  it('escapes values instead of letting them terminate the filter expression', () => {
    const filter = buildDocumentFilter({ ...noFilters, status: "x' || user != '" })
    // The injected quote must arrive escaped, not as a live string terminator.
    expect(filter).toBe("processing_status = 'x\\' || user != \\''")
  })
})

describe('parseDuplicateOfId', () => {
  it('extracts the 15-character record id from a duplicate message', () => {
    expect(parseDuplicateOfId('File is a duplicate of abc123def456ghi.')).toBe('abc123def456ghi')
  })

  it('is case-insensitive on the prefix', () => {
    expect(parseDuplicateOfId('Duplicate of ABC123DEF456GHI')).toBe('ABC123DEF456GHI')
  })

  it('returns null when no id is present', () => {
    expect(parseDuplicateOfId('upload failed')).toBeNull()
    expect(parseDuplicateOfId('duplicate of short')).toBeNull()
  })
})
