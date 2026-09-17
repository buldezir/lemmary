import { describe, expect, it } from 'vitest'
import {
  buildDocumentFilter,
  fileUrlWithToken,
  parseDuplicateOfId,
  uploadErrorMessage,
} from './documents'
import { UNFINISHED_STATUS } from '../documentStatus'

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

  // The timeline's "No date" row, which must reach a null date too.
  it('filters on the absence of a date', () => {
    expect(buildDocumentFilter({ ...noFilters, undated: true })).toBe("document_date = ''")
  })

  it('filters on a status alone', () => {
    expect(buildDocumentFilter({ ...noFilters, status: 'needs_review' })).toBe(
      'processing_status = "needs_review"',
    )
  })

  // Not one status but the absence of completion, so failed and queued
  // documents are in there too.
  it('turns the Inbox filter into everything except completed', () => {
    expect(buildDocumentFilter({ ...noFilters, status: UNFINISHED_STATUS })).toBe(
      'processing_status != "completed"',
    )
  })

  // ALL, not any: a document has to carry every tag the reader picked.
  it('requires every chosen tag', () => {
    expect(buildDocumentFilter({ ...noFilters, tags: ['tag1', 'tag2'] })).toBe(
      'tags ~ "\\"tag1\\"" && tags ~ "\\"tag2\\""',
    )
  })

  it('builds no tag clause for an empty selection', () => {
    expect(buildDocumentFilter({ ...noFilters, tags: [] })).toBeUndefined()
  })

  // Without the quotes this is a bare LIKE '%a%', which keeps every document
  // whose tag ids merely contain the letter.
  it('quotes the id so a short one cannot match inside a longer one', () => {
    expect(buildDocumentFilter({ ...noFilters, tags: ['a'] })).toBe('tags ~ "\\"a\\""')
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
      'processing_status = "failed" && document_date >= "2026-01-01" && document_date < "2026-02-02"',
    )
  })

  it('bounds dateTo exclusively on the next day so the To day is included', () => {
    // Compared as a string, `<= '2025-03-31'` sorts before every timestamp on
    // the 31st, dropping the day a timeline month click selects.
    const stored = '2025-03-31 00:00:00.000Z'
    const filter = buildDocumentFilter({ ...noFilters, dateTo: '2025-03-31' })
    expect(filter).toBe('document_date < "2025-04-01"')
    expect(stored <= '2025-03-31').toBe(false)
    expect(stored < '2025-04-01').toBe(true)
  })

  it('rolls dateTo over month and year ends, and across a leap day', () => {
    const to = (dateTo: string) => buildDocumentFilter({ ...noFilters, dateTo })
    expect(to('2025-12-31')).toBe('document_date < "2026-01-01"')
    expect(to('2024-02-29')).toBe('document_date < "2024-03-01"')
    expect(to('2025-02-28')).toBe('document_date < "2025-03-01"')
  })

  it('passes an unparseable dateTo through rather than inventing a bound', () => {
    expect(buildDocumentFilter({ ...noFilters, dateTo: 'garbage' })).toBe(
      'document_date < "garbage"',
    )
  })

  it('filters by taxonomy record ids', () => {
    expect(
      buildDocumentFilter({ ...noFilters, documentType: 'type123', correspondent: 'corr456' }),
    ).toBe('document_type = "type123" && correspondent = "corr456"')
  })

  it('escapes values instead of letting them terminate the filter expression', () => {
    const filter = buildDocumentFilter({ ...noFilters, status: 'x" || user != "' })
    // The injected quote must arrive escaped, not as a live string terminator.
    expect(filter).toBe('processing_status = "x\\" || user != \\""')
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

describe('fileUrlWithToken', () => {
  // Fetching "" resolves against index.html with a 200 no response check would
  // catch. Refused before the token is minted, so this needs no server.
  it('refuses a document with no file', async () => {
    const record = { id: 'abc123def456ghi', collectionId: 'pbc_1', file: '' }
    await expect(fileUrlWithToken(record)).rejects.toThrow('This document has no file.')
    await expect(fileUrlWithToken(record, '')).rejects.toThrow('This document has no file.')
  })
})

describe('uploadErrorMessage', () => {
  const generic = 'Failed to create record.'

  it('rewords the file size rejection with both sizes', () => {
    const err = {
      response: {
        message: generic,
        data: {
          file: {
            code: 'validation_file_size_limit',
            message: 'Failed to upload x.pdf - the maximum allowed file size is 50000000 bytes.',
          },
        },
      },
    }
    expect(uploadErrorMessage(err, 52_428_800)).toBe(
      'This file is 50 MB, over the 47 MB limit for a single document.',
    )
  })

  it('prefers a field message over the generic one', () => {
    const err = {
      response: {
        message: generic,
        data: { file: { code: 'validation_invalid_mime_type', message: 'Wrong type.' } },
      },
    }
    expect(uploadErrorMessage(err)).toBe('Wrong type.')
  })

  it('keeps a hook message over the data PocketBase rewrote', () => {
    const err = {
      response: {
        message: 'File is a duplicate of abc123def456ghi.',
        data: { duplicate_of: { code: 'validation_invalid_value', message: 'Invalid value.' } },
      },
    }
    expect(uploadErrorMessage(err)).toBe('File is a duplicate of abc123def456ghi.')
  })

  it('falls back to the top-level message, then Error, then a default', () => {
    expect(uploadErrorMessage({ response: { message: generic, data: {} } })).toBe(generic)
    expect(uploadErrorMessage(new Error('boom'))).toBe('boom')
    expect(uploadErrorMessage(undefined)).toBe('Upload failed')
  })
})
