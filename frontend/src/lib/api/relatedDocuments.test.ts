import { afterEach, describe, expect, it, vi } from 'vitest'
import { listRelatedDocuments, searchLinkableDocuments, unlinkDocuments } from './relatedDocuments'
import { pb } from '../pb'

type Call = { method: string; url: URL; body: unknown }

function stubFetch(): Call[] {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({
        method: init?.method ?? 'GET',
        url: new URL(url),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      })
      return Response.json({ id: 'x', page: 1, perPage: 20, totalItems: 0, totalPages: 0, items: [] })
    }),
  )
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
  pb.authStore.clear()
})

describe('unlinkDocuments', () => {
  // A link is stored on whichever document made it, so removal cannot know
  // which side holds it.
  it('removes the link from both documents', async () => {
    const calls = stubFetch()
    await unlinkDocuments('doc_a', 'doc_b')

    const patches = calls
      .filter((call) => call.method === 'PATCH')
      .map((call) => [call.url.pathname, call.body])
      .sort()
    expect(patches).toEqual([
      ['/api/collections/documents/records/doc_a', { 'related-': 'doc_b' }],
      ['/api/collections/documents/records/doc_b', { 'related-': 'doc_a' }],
    ])
  })
})

describe('searchLinkableDocuments', () => {
  it('offers the owner’s other documents, by title once something is typed', async () => {
    pb.authStore.save('test-token', { id: 'me', collectionId: 'users', collectionName: 'users' })
    const calls = stubFetch()
    await searchLinkableDocuments('doc_a', '  ')
    await searchLinkableDocuments('doc_a', 'lease')

    const [empty, typed] = calls.map((call) => call.url.searchParams.get('filter'))
    // Not @request.auth.id: PocketBase refuses that in a filter from anyone but a superuser.
    expect(empty).toBe('user = "me" && id != "doc_a"')
    expect(typed).toBe('user = "me" && id != "doc_a" && title ~ "lease"')
  })
})

describe('listRelatedDocuments', () => {
  const doc = (id: string, date: string) => ({ id, title: id, document_date: date, created: '' })

  function stubRelated(outgoing: unknown[], incoming: unknown[]): URL[] {
    const urls: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        const parsed = new URL(url)
        urls.push(parsed)
        return parsed.pathname.endsWith('/records')
          ? Response.json({ page: 1, perPage: 1000, totalItems: incoming.length, totalPages: 1, items: incoming })
          : Response.json(outgoing.length ? { expand: { related: outgoing } } : {})
      }),
    )
    return urls
  }

  // Neither a filter on documents_via_related (a self-join that timed out on a
  // large archive) nor an expand of it (capped at 1000 rows).
  it('reads outgoing links by expand and incoming ones by filter, once each, newest first', async () => {
    const urls = stubRelated(
      [doc('old', '2020-01-01 00:00:00.000Z'), doc('both', '2022-01-01 00:00:00.000Z')],
      [doc('both', '2022-01-01 00:00:00.000Z'), doc('new', '2024-01-01 00:00:00.000Z')],
    )

    const related = await listRelatedDocuments('doc_a')

    expect(related.map((d) => d.id)).toEqual(['new', 'both', 'old'])
    const record = urls.find((url) => url.pathname === '/api/collections/documents/records/doc_a')
    expect(record?.searchParams.get('expand')).toBe('related')
    expect(record?.searchParams.get('fields')).toBe(
      'expand.related.id,expand.related.title,expand.related.document_date,expand.related.created',
    )
    const list = urls.find((url) => url.pathname === '/api/collections/documents/records')
    expect(list?.searchParams.get('filter')).toBe('related.id ?= "doc_a"')
    expect(list?.searchParams.get('fields')).toBe('id,title,document_date,created')
    expect(urls.some((url) => url.search.includes('documents_via_related'))).toBe(false)
  })

  it('is empty when nothing is linked', async () => {
    stubRelated([], [])
    expect(await listRelatedDocuments('doc_a')).toEqual([])
  })
})
