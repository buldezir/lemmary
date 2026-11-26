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
  // Not a filter on documents_via_related: that joins all documents to
  // themselves and timed out on a large archive.
  it('expands both directions of the one document, once each, newest first', async () => {
    const doc = (id: string, date: string) => ({ id, title: id, document_date: date, created: '' })
    const urls: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(new URL(url))
        return Response.json({
          expand: {
            related: [doc('old', '2020-01-01 00:00:00.000Z'), doc('both', '2022-01-01 00:00:00.000Z')],
            documents_via_related: [doc('both', '2022-01-01 00:00:00.000Z'), doc('new', '2024-01-01 00:00:00.000Z')],
          },
        })
      }),
    )

    const related = await listRelatedDocuments('doc_a')

    expect(related.map((d) => d.id)).toEqual(['new', 'both', 'old'])
    expect(urls).toHaveLength(1)
    expect(urls[0].pathname).toBe('/api/collections/documents/records/doc_a')
    expect(urls[0].searchParams.get('expand')).toBe('related,documents_via_related')
    expect(urls[0].searchParams.get('filter')).toBeNull()
  })

  it('is empty when nothing is linked', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({})))
    expect(await listRelatedDocuments('doc_a')).toEqual([])
  })
})
