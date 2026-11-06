import { afterEach, expect, test, vi } from 'vitest'
import { createToken, listTokens, revokeToken } from './tokens'
import { pb } from '../pb'

afterEach(() => {
  vi.unstubAllGlobals()
  pb.authStore.clear()
})

test('lists, creates and revokes tokens of one scope', async () => {
  const payload = btoa(JSON.stringify({ exp: 4102444800 }))
  pb.authStore.save(`h.${payload}.s`, { id: 'me', collectionId: 'users', collectionName: 'users' })
  const calls: { url: URL; method: string; body: unknown }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ url: new URL(url, 'http://x'), method: init.method ?? 'GET', body: init.body ? JSON.parse(String(init.body)) : null })
    if (init.method === 'DELETE') return new Response(null, { status: 204 })
    return Response.json({ tokens: [{ id: 'a', name: 'Claude Code', created: '2026-11-06 10:00:00.000Z', last_used: '' }], token: 'lmy_x' })
  }))
  expect(await listTokens('mcp')).toHaveLength(1)
  expect(await createToken('paperless')).toBe('lmy_x')
  await revokeToken('a')
  expect(calls[0].url.pathname + calls[0].url.search).toBe('/api/app/tokens?scope=mcp')
  expect(calls[1].method).toBe('POST')
  expect(calls[1].body).toEqual({ scope: 'paperless', name: '' })
  expect(calls[2].method).toBe('DELETE')
  expect(calls[2].url.pathname).toBe('/api/app/tokens/a')
})
