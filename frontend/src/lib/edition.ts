import type { AnyRoute } from '@tanstack/react-router'

/**
 * An entry an edition adds to the "More" navigation menu.
 *
 * The menu rather than the main navigation bar: the header holds three links by
 * design and stays legible at narrow widths only because it does. Editions add
 * pages; they should not get to reshape the primary navigation.
 */
export type EditionNavItem = {
  /** Route path, e.g. '/billing'. Must match a route the edition contributes. */
  to: string
  label: string
  /** When true the item is rendered only for an admin. */
  adminOnly?: boolean
}

/**
 * Everything a build of the frontend adds to the core application.
 *
 * The open-source build supplies the empty edition in `src/ext/index.ts`. A
 * private build supplies its own module at the `@ext` alias, so a fork adds a
 * directory rather than editing shared files and never has a merge conflict
 * here. See `vite.config.ts` for how the alias is resolved.
 */
export type Edition = {
  /** Shown nowhere; used by tests and by the build log to identify the build. */
  name: string
  /**
   * Routes appended as children of the root route.
   *
   * A function rather than an array because every route needs the root route as
   * its parent, and the root route is created inside `router.tsx`.
   */
  routes: (rootRoute: AnyRoute) => AnyRoute[]
  navItems: EditionNavItem[]
}

/** The edition that adds nothing. Also the shape every edition must satisfy. */
export const emptyEdition: Edition = {
  name: '',
  routes: () => [],
  navItems: [],
}
