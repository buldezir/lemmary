import { afterEach, describe, expect, it, vi } from 'vitest'
import { listActiveJobs } from './jobs'
import { pb } from '../pb'

describe('listActiveJobs', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    pb.authStore.clear()
  })

  function signIn() {
    const payload = btoa(JSON.stringify({ exp: 4102444800 }))
    pb.authStore.save(`h.${payload}.s`, { id: 'me', collectionId: 'users', collectionName: 'users' })
  }

  // The queue and the failures as the server would answer each: the queue
  // oldest first, the failures newest first.
  function stubServer(queue: object[], queueTotal: number, failures: object[]) {
    const params: URLSearchParams[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        const search = new URL(url).searchParams
        params.push(search)
        const items = search.get('filter') === "finished_at = ''" ? queue : failures
        const totalItems = items === queue ? queueTotal : failures.length
        return Response.json({ page: 1, perPage: 100, totalItems, items })
      }),
    )
    return params
  }

  it('asks for the queue oldest first and for standing failures newest first', async () => {
    signIn()
    const params = stubServer([], 0, [])

    await listActiveJobs()
    const failures = params.find((search) => search.get('filter') !== "finished_at = ''")!
    expect(params.find((search) => search.get('filter') === "finished_at = ''")?.get('sort')).toBe('created')
    expect(failures.get('sort')).toBe('-created')
    expect(failures.get('filter')).toContain('status = "failed" && document.processing_status = "failed"')
    expect(failures.get('filter')).toContain(
      'status = "cancelled" && document.processing_status = "cancelled"',
    )
  })

  it('keeps the newest failure per document, after the whole queue page', async () => {
    signIn()
    stubServer(
      [{ id: 'run', document: 'b', status: 'running', finished_at: '' }],
      150,
      [
        { id: 'new', document: 'a', status: 'failed', finished_at: '2026-03-01 11:00:00.000Z' },
        { id: 'old', document: 'a', status: 'failed', finished_at: '2026-03-01 10:00:00.000Z' },
      ],
    )

    const { jobs, total } = await listActiveJobs()
    expect(jobs.map((job) => job.id)).toEqual(['run', 'new'])
    expect(total).toBe(151)
  })
})
