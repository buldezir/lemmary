import { afterEach, describe, expect, test } from 'vitest'
import { setAlwaysRequireReview } from './reviewPolicy'
import {
  defaultDocumentQuery,
  documentQuerySearch,
  hasActiveFilters,
  inboxQuerySearch,
  parseDocumentQuery,
} from './documentQuery'

describe('parseDocumentQuery', () => {
  test('an empty query string is the unfiltered list', () => {
    expect(parseDocumentQuery({})).toEqual(defaultDocumentQuery)
  })

  test('reads every filter back out of the URL', () => {
    expect(
      parseDocumentQuery({
        q: 'invoice',
        status: 'failed',
        from: '2025-01-01',
        to: '2025-03-31',
        type: 'abc123',
        correspondent: 'def456',
        page: '3',
      }),
    ).toEqual({
      q: 'invoice',
      status: 'failed',
      from: '2025-01-01',
      to: '2025-03-31',
      type: 'abc123',
      correspondent: 'def456',
      page: 3,
    })
  })

  test('an unknown status is dropped rather than sent to the server', () => {
    expect(parseDocumentQuery({ status: 'exploded' }).status).toBe('all')
  })

  test('a malformed date is dropped', () => {
    expect(parseDocumentQuery({ from: 'yesterday', to: '2025-3-1' })).toMatchObject({
      from: '',
      to: '',
    })
  })

  test('an id has to look like an id', () => {
    expect(parseDocumentQuery({ type: "a' || 1=1 --" }).type).toBe('all')
  })

  test('page is a whole number of at least one', () => {
    expect(parseDocumentQuery({ page: '0' }).page).toBe(1)
    expect(parseDocumentQuery({ page: '-2' }).page).toBe(1)
    expect(parseDocumentQuery({ page: '1.5' }).page).toBe(1)
    expect(parseDocumentQuery({ page: 'two' }).page).toBe(1)
    expect(parseDocumentQuery({ page: 7 }).page).toBe(7)
  })

  test('non-string junk is ignored', () => {
    expect(parseDocumentQuery({ q: ['a'], status: 3, from: null })).toEqual(defaultDocumentQuery)
  })
})

describe('documentQuerySearch', () => {
  test('an unfiltered list writes no query string at all', () => {
    expect(documentQuerySearch(defaultDocumentQuery)).toEqual({})
  })

  test('only the filters that are set are written', () => {
    expect(
      documentQuerySearch({ ...defaultDocumentQuery, status: 'failed', page: 2 }),
    ).toEqual({ status: 'failed', page: 2 })
  })

  test('round-trips', () => {
    const query = {
      q: 'rent',
      status: 'completed',
      from: '2024-06-01',
      to: '2024-06-30',
      type: 'typ1',
      correspondent: 'cor1',
      page: 4,
    }
    expect(parseDocumentQuery(documentQuerySearch(query))).toEqual(query)
  })
})

// /inbox holds its status in the path, so the query string must never carry
// one -- neither a matching one, which would be noise on every link, nor a
// conflicting one, which would silently show a different list.
describe('inboxQuerySearch', () => {
  test('drops the status whatever it says', () => {
    expect(inboxQuerySearch({ status: 'needs_review' })).toEqual({})
    expect(inboxQuerySearch({ status: 'failed' })).toEqual({})
    expect(inboxQuerySearch({ status: 'nonsense' })).toEqual({})
  })

  test('keeps every other filter', () => {
    expect(
      inboxQuerySearch({
        q: 'rent',
        status: 'completed',
        from: '2024-06-01',
        to: '2024-06-30',
        type: 'typ1',
        correspondent: 'cor1',
        page: 4,
      }),
    ).toEqual({
      q: 'rent',
      from: '2024-06-01',
      to: '2024-06-30',
      type: 'typ1',
      correspondent: 'cor1',
      page: 4,
    })
  })

  test('leaves a bare Inbox URL bare', () => {
    expect(inboxQuerySearch({})).toEqual({})
  })
})

// With review required, `/` defaults to Completed rather than to everything:
// the Inbox is the pile, and `/` is the archive that has been read. Parsing and
// serializing have to agree about that, or the round-trip below breaks and the
// dropdown snaps back to Completed the moment "All statuses" is picked.
describe('with review required for every new document', () => {
  afterEach(() => setAlwaysRequireReview(false))

  test('a bare URL means the reviewed archive', () => {
    setAlwaysRequireReview(true)
    expect(parseDocumentQuery({}).status).toBe('completed')
  })

  test('Completed is the bare URL, and All statuses is the explicit one', () => {
    setAlwaysRequireReview(true)
    expect(documentQuerySearch({ ...defaultDocumentQuery, status: 'completed' })).toEqual({})
    expect(documentQuerySearch({ ...defaultDocumentQuery, status: 'all' })).toEqual({
      status: 'all',
    })
  })

  test('All statuses survives the round-trip that used to strip it', () => {
    setAlwaysRequireReview(true)
    const search = documentQuerySearch({ ...defaultDocumentQuery, status: 'all' })
    expect(parseDocumentQuery(search).status).toBe('all')
  })

  test('the Inbox still carries no status either way', () => {
    setAlwaysRequireReview(true)
    expect(inboxQuerySearch({ status: 'completed' })).toEqual({})
    expect(inboxQuerySearch({ status: 'all' })).toEqual({})
    expect(inboxQuerySearch({ status: 'needs_review', page: 3 })).toEqual({ page: 3 })
  })

  // Off, everything is exactly as it was: 'all' is the default and vanishes.
  test('changes nothing while it is off', () => {
    expect(parseDocumentQuery({}).status).toBe('all')
    expect(documentQuerySearch({ ...defaultDocumentQuery, status: 'all' })).toEqual({})
    expect(documentQuerySearch({ ...defaultDocumentQuery, status: 'completed' })).toEqual({
      status: 'completed',
    })
  })
})

describe('hasActiveFilters', () => {
  test('paging alone is not a filter', () => {
    expect(hasActiveFilters({ ...defaultDocumentQuery, page: 3 })).toBe(false)
  })

  test('a search term is', () => {
    expect(hasActiveFilters({ ...defaultDocumentQuery, q: 'x' })).toBe(true)
  })

  test('so is a date bound on its own', () => {
    expect(hasActiveFilters({ ...defaultDocumentQuery, from: '2025-01-01' })).toBe(true)
  })
})
