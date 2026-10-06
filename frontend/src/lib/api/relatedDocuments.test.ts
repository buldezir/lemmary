import { afterEach, describe, expect, it, vi } from 'vitest'
import { searchLinkableDocuments, unlinkDocuments } from './relatedDocuments'
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
