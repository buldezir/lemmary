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
      "processing_status = 'failed' && document_date >= '2026-01-01' && document_date <= '2026-02-01'",
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
