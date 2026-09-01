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
      '/upload',
      '/upload/amazon',
      '/upload/split',
      '/search',
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
    ]) {
      expect(paths).toContain(path)
    }
  })
})
