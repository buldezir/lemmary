import type { LinkProps } from '@tanstack/react-router'

/**
 * The header's link set, as data.
 *
 * The bar and the mobile panel render the same items in two very different
 * shapes, and a link added to only one of them is the failure mode that keeps
 * happening -- so the list lives here once and both walk it.
 */
export type RouteNavItem = {
  kind: 'route'
  label: string
  to: LinkProps['to']
  /** Marks the link active only on an exact path match. */
  exact?: boolean
  /** Rendered for admins only, and labelled with a shield. */
  admin?: boolean
}

export type ExternalNavItem = {
  kind: 'external'
  label: string
  href: string
  admin?: boolean
}

export type NavItem = RouteNavItem | ExternalNavItem

/** The links that sit in the header bar itself on a wide viewport. */
export const primaryNavItems: readonly NavItem[] = [
  { kind: 'route', label: 'Documents', to: '/', exact: true },
  { kind: 'route', label: 'Upload', to: '/upload' },
  // /rag, not a mode: it is the one path above both, so this marks itself
  // active in Search and Research alike.
  { kind: 'route', label: 'Deep Search', to: '/rag' },
]

/** The links behind the bar's "More" menu, listed inline on a narrow one. */
export function secondaryNavItems(pbAdminUrl: string): readonly NavItem[] {
  return [
    { kind: 'route', label: 'Account', to: '/account' },
    { kind: 'route', label: 'OCR test', to: '/ocr-test' },
    { kind: 'route', label: 'Export', to: '/export' },
    { kind: 'route', label: 'Import', to: '/import' },
    { kind: 'route', label: 'Settings', to: '/settings', admin: true },
    { kind: 'route', label: 'Management', to: '/management', admin: true },
    { kind: 'external', label: 'Admin', href: pbAdminUrl, admin: true },
  ]
}

/**
 * Drops the admin-only items for a regular user. The admin routes bounce
 * non-admins anyway; this is about not offering a dead end.
 */
export function visibleNavItems(items: readonly NavItem[], admin: boolean): NavItem[] {
  return items.filter((item) => !item.admin || admin)
}
