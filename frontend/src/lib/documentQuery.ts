/**
 * The document list's filters, held in the URL rather than in component state,
 * so the URL is their one copy.
 *
 * Everything arriving from the URL is untrusted, hand-edited or left over from
 * an older build, so an unrecognised value falls back to its default. The worst
 * a bad URL can do is show the unfiltered list.
 */

import { isDocumentStatus } from './documentStatus'

/** Matches the index's prefix floor, fulltext.minPrefixLen. */
export const MIN_SEARCH_LENGTH = 3

export function searchableTerm(value: string): string {
  const term = value.trim()
  return term.length < MIN_SEARCH_LENGTH ? '' : term
}

export type DocumentQuery = {
  /** Fulltext search; empty means list everything. */
  q: string
  /** A processing_status, or 'all'. */
  status: string
  /** Inclusive "YYYY-MM-DD" bounds; empty means unbounded. */
  from: string
  to: string
  /** Only the documents with no document_date, which no From/To can reach. */
  undated: boolean
  /** A document_types id, or 'all'. */
  type: string
  /** A correspondents id, or 'all'. */
  correspondent: string
  /** Comma-joined tags ids a document must carry all of; empty means any. */
  tags: string
  /** Only the documents with no tags. */
  untagged: boolean
  /** 1-based. */
  page: number
}

/**
 * What may actually arrive: every filter optional, and none of them trustworthy.
 * Optional is what lets the rest of the app keep linking to a plain `/`.
 */
export type DocumentQueryInput = Partial<Record<keyof DocumentQuery, unknown>>

export const defaultDocumentQuery: DocumentQuery = {
  q: '',
  status: 'all',
  from: '',
  to: '',
  undated: false,
  type: 'all',
  correspondent: 'all',
  tags: '',
  untagged: false,
  page: 1,
}

const datePattern = /^\d{4}-\d{2}-\d{2}$/

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function date(value: unknown): string {
  const raw = text(value)
  return datePattern.test(raw) ? raw : ''
}

/**
 * Only the shape is enforced: the lists load later, and asking for a deleted id
 * simply matches no documents.
 */
function id(value: unknown): string {
  const raw = text(value)
  return raw && /^[a-zA-Z0-9]+$/.test(raw) ? raw : 'all'
}

/** The ids in a `?tags=` value, dropping anything malformed or repeated. */
export function tagIds(value: unknown): string[] {
  return [...new Set(text(value).split(',').filter((part) => /^[a-zA-Z0-9]+$/.test(part)))]
}

function pageNumber(value: unknown): number {
  const raw = typeof value === 'number' ? value : Number(text(value))
  return Number.isInteger(raw) && raw >= 1 ? raw : 1
}

/** Reads a URL's query string into filters, defaulting anything unusable. */
export function parseDocumentQuery(raw: DocumentQueryInput): DocumentQuery {
  const status = text(raw.status)
  return {
    q: typeof raw.q === 'string' ? searchableTerm(raw.q) : '',
    // 'all' is the absence of a status filter, so it is not in
    // DOCUMENT_STATUSES, but a URL may name it.
    status: isDocumentStatus(status) || status === 'all' ? status : defaultDocumentQuery.status,
    from: date(raw.from),
    to: date(raw.to),
    // A URL carries it as the string; the router hands it back as the boolean.
    undated: raw.undated === true || raw.undated === 'true',
    type: id(raw.type),
    correspondent: id(raw.correspondent),
    tags: tagIds(raw.tags).join(','),
    untagged: raw.untagged === true || raw.untagged === 'true',
    page: pageNumber(raw.page),
  }
}

/**
 * Only the filters that differ from their default, so an unfiltered list is
 * plain "/". This is what the route validates *to*, so returning the full set
 * would stamp "?q=&status=all&page=1" onto every link back to the list.
 */
export function documentQuerySearch(query: DocumentQuery): Partial<DocumentQuery> {
  const search: Partial<DocumentQuery> = {}
  for (const key of Object.keys(defaultDocumentQuery) as (keyof DocumentQuery)[]) {
    if (query[key] !== defaultDocumentQuery[key]) {
      Object.assign(search, { [key]: query[key] })
    }
  }
  return search
}

/**
 * The Inbox carries a page and nothing else: it has no filter controls, and a
 * hand-typed `?from=2026-01-01` would quietly shorten it with nothing on screen
 * to blame. Its status is its path.
 */
export function inboxQuerySearch(raw: DocumentQueryInput): Partial<DocumentQuery> {
  return documentQuerySearch({
    ...defaultDocumentQuery,
    page: parseDocumentQuery(raw).page,
  })
}

/** Whether the list is narrowed at all, which decides the empty-state wording. */
export function hasActiveFilters(query: DocumentQuery): boolean {
  return Object.keys(documentQuerySearch(query)).some((key) => key !== 'page')
}
