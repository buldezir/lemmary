/**
 * The document list's filters, held in the URL rather than in component state.
 *
 * Every control on the documents page writes here, so a reload, a bookmark, a
 * shared link and the Back button all land on the same list. The URL is the one
 * copy of the filters: the page reads them back out of it instead of keeping a
 * second copy in useState that a refresh would throw away.
 *
 * Everything arriving from the URL is untrusted — it can be hand-edited, or
 * left over from an older build — so an unrecognised value falls back to its
 * default instead of reaching the query. The worst a bad URL can do is show the
 * unfiltered list.
 */

import { isDocumentStatus } from './documentStatus'

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
 * Type and correspondent are record ids, so there is nothing to check them
 * against here — the lists load later, and asking for a deleted id simply
 * matches no documents. Only the shape is enforced.
 */
function id(value: unknown): string {
  const raw = text(value)
  return raw && /^[a-zA-Z0-9]+$/.test(raw) ? raw : 'all'
}

function pageNumber(value: unknown): number {
  const raw = typeof value === 'number' ? value : Number(text(value))
  return Number.isInteger(raw) && raw >= 1 ? raw : 1
}

/** Reads a URL's query string into filters, defaulting anything unusable. */
export function parseDocumentQuery(raw: DocumentQueryInput): DocumentQuery {
  const status = text(raw.status)
  return {
    q: typeof raw.q === 'string' ? raw.q : '',
    // 'all' is the absence of a status filter rather than one of them, so it
    // is not in DOCUMENT_STATUSES -- but a URL may name it.
    status: isDocumentStatus(status) || status === 'all' ? status : defaultDocumentQuery.status,
    from: date(raw.from),
    to: date(raw.to),
    // A URL carries it as the string; the router hands it back as the boolean.
    undated: raw.undated === true || raw.undated === 'true',
    type: id(raw.type),
    correspondent: id(raw.correspondent),
    page: pageNumber(raw.page),
  }
}

/**
 * The other direction: only the filters that differ from their default are
 * kept, so an unfiltered list is plain "/" and a shared link carries just the
 * parts that matter. This is also what the route validates *to*: whatever it
 * returns is the URL, so returning the full set would stamp
 * "?q=&status=all&page=1" onto every plain link back to the list.
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
 * The same, for the Inbox route, which carries a page and nothing else.
 *
 * The Inbox has no filter controls: it is a tray worked through until it is
 * empty, and narrowing it would only hide work still to do. Enforced here
 * rather than in the page, because the alternative is a hand-typed
 * `?from=2026-01-01` quietly shortening a list with nothing on screen to blame
 * it on. Its status is its path, so a `status` param could only repeat that or
 * contradict it.
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
