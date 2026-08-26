import { createRoute } from '@tanstack/react-router'
import type { AnyRoute } from '@tanstack/react-router'
import type { Edition } from '../lib/edition'
import { EditionProbePage } from './EditionProbePage'

/**
 * The throwaway frontend edition, selected by `EDITION=exttest`.
 *
 * It is the counterpart of internal/appwire/edition_exttest.go and exists for
 * the same reason: the real private edition lives in a fork this repository
 * cannot build, so without a stand-in nothing here would notice the `@ext`
 * alias, the Dockerfile's EDITION argument, or the route spread in router.tsx
 * breaking. Twenty lines that keep `docker build --build-arg EDITION=exttest`
 * honest.
 */
export const edition: Edition = {
  name: 'exttest',
  routes: (rootRoute: AnyRoute) => [
    createRoute({
      getParentRoute: () => rootRoute,
      path: '/edition-probe',
      component: EditionProbePage,
    }),
  ],
  navItems: [{ to: '/edition-probe', label: 'Edition probe' }],
}
