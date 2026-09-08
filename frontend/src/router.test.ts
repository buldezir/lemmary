import { describe, expect, test } from 'vitest'

// The route tree is a hand-written file rather than a generated one, so a page
// reaches the app only because somebody added a line to it — and that line is
// easy to drop while tidying. This asserts the tree actually holds every path
// the navigation links to.
describe('route tree', () => {
  test('includes every core route', async () => {
    const { router } = await import('./router')
    const paths = Object.keys(router.routesById)

    for (const path of [
      '/',
      '/inbox',
      '/upload',
      '/upload/amazon',
      '/upload/split',
      '/rag/search',
      '/rag/search/$sessionId',
      '/rag/research',
      '/rag/research/$sessionId',
      '/ocr-test',
      '/settings',
      '/management',
      '/import',
      '/import/ngx',
      '/import/archive',
      '/account',
      '/export',
      '/document/$documentId',
      '/document/$documentId/ask',
      '/document/$documentId/ask/$sessionId',
    ]) {
      expect(paths).toContain(path)
    }
  })
})

// The document list keeps its filters in the query string, which only works
// because the route declares a validator. Without it the page reads undefined
// filters and every reload comes back unfiltered.
describe('document list search params', () => {
  test('are validated by the route, keeping only what is set', async () => {
    const { router } = await import('./router')
    const validate = router.routesById['/'].options.validateSearch as
      | ((search: Record<string, unknown>) => unknown)
      | undefined

    // Whatever this returns becomes the URL, so an unfiltered list has to come
    // back empty: every plain link to "/" in the app goes through here, and
    // filling in the defaults would hang "?q=&status=all&page=1" off all of them.
    expect(validate?.({})).toEqual({})
    expect(validate?.({ status: 'failed', page: 2, nonsense: 'x' })).toEqual({
      status: 'failed',
      page: 2,
    })
  })

  // /inbox is the needs_review list by definition, so a status in its URL could
  // only repeat the path or contradict it. Dropping it at the validator is what
  // makes it unlinkable and untypeable.
  test('drop a status on the Inbox route while keeping the other filters', async () => {
    const { router } = await import('./router')
    const validate = router.routesById['/inbox'].options.validateSearch as
      | ((search: Record<string, unknown>) => unknown)
      | undefined

    expect(validate?.({})).toEqual({})
    expect(validate?.({ status: 'failed', page: 2 })).toEqual({ page: 2 })
    expect(validate?.({ status: 'needs_review' })).toEqual({})
    expect(validate?.({ q: 'invoice', from: '2026-01-01' })).toEqual({
      q: 'invoice',
      from: '2026-01-01',
    })
  })
})
