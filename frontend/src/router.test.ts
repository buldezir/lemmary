import { createRoute } from '@tanstack/react-router'
import { describe, expect, test, vi } from 'vitest'

// Stands in for a private edition. The point of the test is not this module but
// router.tsx: an edition contributes routes only because the route tree spreads
// them in, and that spread is one line somebody can drop while tidying.
vi.mock('@ext', () => ({
  edition: {
    name: 'router-test',
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    routes: (rootRoute: any) => [
      createRoute({
        getParentRoute: () => rootRoute,
        path: '/edition-only',
        component: () => null,
      }),
    ],
    navItems: [{ to: '/edition-only', label: 'Edition only' }],
  },
}))

describe('route tree', () => {
  test('includes the edition routes', async () => {
    const { router } = await import('./router')

    expect(Object.keys(router.routesById)).toContain('/edition-only')
  })

  test('still includes the core routes', async () => {
    const { router } = await import('./router')
    const paths = Object.keys(router.routesById)

    for (const path of ['/', '/upload', '/search', '/settings', '/import', '/import/archive']) {
      expect(paths).toContain(path)
    }
  })
})
