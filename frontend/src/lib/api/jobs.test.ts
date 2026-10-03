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

  it('asks only for failures the document still has', async () => {
    signIn()
    const urls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(url)
        return Response.json({ page: 1, perPage: 100, totalItems: 0, items: [] })
      }),
    )

    await listActiveJobs()
    const filter = new URL(urls[0]).searchParams.get('filter') ?? ''
    expect(filter).toContain('status = "failed" && document.processing_status = "failed"')
    expect(filter).toContain('status = "cancelled" && document.processing_status = "cancelled"')
  })

  it('keeps the newest failure per document and every unfinished job', async () => {
    signIn()
    const items = [
      { id: 'old', document: 'a', status: 'failed', finished_at: '2026-03-01 10:00:00.000Z' },
      { id: 'run', document: 'b', status: 'running', finished_at: '' },
      { id: 'new', document: 'a', status: 'failed', finished_at: '2026-03-01 11:00:00.000Z' },
    ]
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json({ page: 1, perPage: 100, totalItems: 150, items })),
    )

    const { jobs, total } = await listActiveJobs()
    expect(jobs.map((job) => job.id)).toEqual(['run', 'new'])
    expect(total).toBe(149)
  })
})
