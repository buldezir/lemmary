import { describe, expect, test } from 'vitest'
import {
  defaultDocumentQuery,
  documentQuerySearch,
  hasActiveFilters,
  inboxQuerySearch,
  parseDocumentQuery,
  searchableTerm,
  tagIds,
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
        tags: 'tag1,tag2',
        undated: 'true',
        page: '3',
      }),
    ).toEqual({
      q: 'invoice',
      status: 'failed',
      from: '2025-01-01',
      to: '2025-03-31',
      type: 'abc123',
      correspondent: 'def456',
      tags: 'tag1,tag2',
      untagged: false,
      undated: true,
      page: 3,
    })
  })

  test('keeps only the usable tag ids, once each', () => {
    expect(parseDocumentQuery({ tags: 'tag1,,tag 2,tag1,tag3' }).tags).toBe('tag1,tag3')
  })

  test('an all-junk tags value shows the unfiltered list', () => {
    expect(parseDocumentQuery({ tags: ' ,;,--' }).tags).toBe('')
    expect(parseDocumentQuery({ tags: 42 }).tags).toBe('')
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
      tags: 'tag1,tag2',
      untagged: false,
      undated: false,
      page: 4,
    }
    expect(parseDocumentQuery(documentQuerySearch(query))).toEqual(query)
  })
})

// /inbox holds its status in the path, so a query-string status could only be
// noise or a contradiction.
describe('inboxQuerySearch', () => {
  test('drops the status whatever it says', () => {
    expect(inboxQuerySearch({ status: 'needs_review' })).toEqual({})
    expect(inboxQuerySearch({ status: 'failed' })).toEqual({})
    expect(inboxQuerySearch({ status: 'nonsense' })).toEqual({})
  })

  test('drops every filter, keeping only the page', () => {
    expect(
      inboxQuerySearch({
        q: 'rent',
        status: 'completed',
        from: '2024-06-01',
        to: '2024-06-30',
        type: 'typ1',
        correspondent: 'cor1',
        tags: 'tag1,tag2',
        page: 4,
      }),
    ).toEqual({ page: 4 })
  })

  test('leaves a bare Inbox URL bare', () => {
    expect(inboxQuerySearch({})).toEqual({})
  })
})

describe('the undated filter', () => {
  test('survives the URL as a string', () => {
    expect(parseDocumentQuery({ undated: 'true' }).undated).toBe(true)
  })

  test('is off unless it says true', () => {
    expect(parseDocumentQuery({ undated: 'yes' }).undated).toBe(false)
    expect(parseDocumentQuery({}).undated).toBe(false)
  })

  test('stays out of the URL when off', () => {
    expect(documentQuerySearch({ ...defaultDocumentQuery, undated: false })).toEqual({})
    expect(documentQuerySearch({ ...defaultDocumentQuery, undated: true })).toEqual({
      undated: true,
    })
  })
})

describe('the untagged filter', () => {
  test('survives the URL as a string and stays out of it when off', () => {
    expect(parseDocumentQuery({ untagged: 'true' }).untagged).toBe(true)
    expect(parseDocumentQuery({ untagged: 'yes' }).untagged).toBe(false)
    expect(documentQuerySearch({ ...defaultDocumentQuery, untagged: false })).toEqual({})
    expect(documentQuerySearch({ ...defaultDocumentQuery, untagged: true })).toEqual({
      untagged: true,
    })
    expect(hasActiveFilters({ ...defaultDocumentQuery, untagged: true })).toBe(true)
  })
})

test('untagged drops tags it could never match', () => {
  expect(parseDocumentQuery({ tags: 'tag1', untagged: 'true' })).toMatchObject({
    tags: '',
    untagged: true,
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

  // It decides the empty-state wording, which must not say "no documents yet"
  // to someone who has only narrowed the list to a tag.
  test('so is a tag', () => {
    expect(hasActiveFilters({ ...defaultDocumentQuery, tags: 'tag1' })).toBe(true)
  })
})

describe('tagIds', () => {
  test('splits a comma-joined value', () => {
    expect(tagIds('tag1,tag2')).toEqual(['tag1', 'tag2'])
  })

  test('is empty for anything unusable, so no clause is built', () => {
    expect(tagIds('')).toEqual([])
    expect(tagIds(undefined)).toEqual([])
  })
})

describe('searchableTerm', () => {
  test('keeps a term long enough to search for, trimmed', () => {
    expect(searchableTerm('  amaz ')).toBe('amaz')
    expect(searchableTerm('ama')).toBe('ama')
  })

  test('drops a term still being typed', () => {
    expect(searchableTerm('am')).toBe('')
    expect(searchableTerm('a')).toBe('')
    expect(searchableTerm('  b ')).toBe('')
  })

  test('measures the trimmed term', () => {
    expect(searchableTerm('a  ')).toBe('')
  })

  test('a hand-typed short ?q= shows the unfiltered list rather than filtering', () => {
    expect(parseDocumentQuery({ q: 'am' }).q).toBe('')
    expect(parseDocumentQuery({ q: 'amaz' }).q).toBe('amaz')
  })
})
