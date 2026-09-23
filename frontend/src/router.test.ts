import { describe, expect, test } from 'vitest'

// The route tree is hand-written, so a page reaches the app only because
// somebody added a line to it. This asserts it holds every path nav links to.
describe('route tree', () => {
  test('includes every core route', async () => {
    const { router } = await import('./router')
    const paths = Object.keys(router.routesById)

    for (const path of [
      '/',
      '/inbox',
      '/activity',
      '/upload',
      '/upload/scan',
      '/upload/amazon',
      '/upload/zip',
      '/upload/split',
      '/rag/search',
      '/rag/search/$sessionId',
      '/rag/research',
      '/rag/research/$sessionId',
      '/ocr-test',
      '/settings',
      '/settings/ai',
      '/settings/processing',
      '/settings/worker',
      '/settings/duplicates',
      '/settings/ingest',
      '/management',
      '/import',
      '/import/ngx',
      '/import/archive',
      '/account',
      '/tags',
      '/export',
      '/document/$documentId',
      '/document/$documentId/ask',
      '/document/$documentId/ask/$sessionId',
    ]) {
      expect(paths).toContain(path)
    }
  })
})

// Without the route's validator the page reads undefined filters and every
// reload comes back unfiltered.
describe('document list search params', () => {
  test('are validated by the route, keeping only what is set', async () => {
    const { router } = await import('./router')
    const validate = router.routesById['/'].options.validateSearch as
      | ((search: Record<string, unknown>) => unknown)
      | undefined

    // Whatever this returns becomes the URL, so filling in the defaults would
    // hang "?q=&status=all&page=1" off every plain link to "/".
    expect(validate?.({})).toEqual({})
    expect(validate?.({ status: 'failed', page: 2, nonsense: 'x' })).toEqual({
      status: 'failed',
      page: 2,
    })
  })

  // The Inbox has no filter controls, so dropping the rest at the validator is
  // what makes a hand-typed ?from= unable to shorten the tray.
  test('strip every filter on the Inbox route, keeping the page', async () => {
    const { router } = await import('./router')
    const validate = router.routesById['/inbox'].options.validateSearch as
      | ((search: Record<string, unknown>) => unknown)
      | undefined

    expect(validate?.({})).toEqual({})
    expect(validate?.({ status: 'failed', page: 2 })).toEqual({ page: 2 })
    expect(validate?.({ status: 'needs_review' })).toEqual({})
    expect(validate?.({ q: 'invoice', from: '2026-01-01' })).toEqual({})
    expect(validate?.({ tags: 'tag1,tag2' })).toEqual({})
  })
})
