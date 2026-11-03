import { afterEach, expect, test, vi } from 'vitest'
import { listLibraryShares, revokeLibraryShare, shareLibrary } from './libraryShares'
import { pb } from '../pb'

afterEach(() => {
  vi.unstubAllGlobals()
  pb.authStore.clear()
})

test('lists the requested owner and creates grants for the signed-in owner', async () => {
  pb.authStore.save('token', { id: 'owner', collectionId: 'users', collectionName: 'users' })
  const calls: { url: URL; method: string; body: unknown }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ url: new URL(url), method: init.method ?? 'GET', body: init.body ? JSON.parse(String(init.body)) : null })
    return Response.json({ items: [], totalPages: 1 })
  }))
  await listLibraryShares()
  await listLibraryShares('another-owner')
  await shareLibrary('reader')
  await revokeLibraryShare('grant')
  expect(calls[0].url.searchParams.get('filter')).toBe('owner = "owner"')
  expect(calls[1].url.searchParams.get('filter')).toBe('owner = "another-owner"')
  expect(calls[2].method).toBe('POST')
  expect(calls[2].body).toEqual({ owner: 'owner', recipient: 'reader' })
  expect(calls[3].method).toBe('DELETE')
  expect(calls[3].url.pathname).toBe('/api/collections/library_shares/records/grant')
})

test('does not list grants without an owner', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  expect(await listLibraryShares()).toEqual([])
  expect(fetch).not.toHaveBeenCalled()
})
