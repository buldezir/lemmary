import type { LinkProps } from '@tanstack/react-router'

/**
 * The header's link set as data: the bar and the mobile panel render it in two
 * shapes, and a link added to only one of them was the recurring failure.
 */

/** Names a count the header hangs off a link -- the key, never the number. */
export type NavBadgeKey = 'inbox' | 'activity'

/**
 * What a badge's digits mean, spelled out for assistive tech -- "Inbox 3" is
 * not a useful accessible name. Rendered after the count.
 */
export const NAV_BADGE_DESCRIPTIONS: Record<NavBadgeKey, string> = {
  inbox: 'not finished processing',
  activity: 'processing',
}

export type RouteNavItem = {
  kind: 'route'
  label: string
  to: LinkProps['to']
  /** Marks the link active only on an exact path match. */
  exact?: boolean
  /** Rendered for admins only, and labelled with a shield. */
  admin?: boolean
  /** Which count to show beside the label, if any. */
  badgeKey?: NavBadgeKey
}

export type ExternalNavItem = {
  kind: 'external'
  label: string
  href: string
  admin?: boolean
}

export type NavItem = RouteNavItem | ExternalNavItem

/**
 * A function because the Inbox is conditional: without always_require_review a
 * document reaches needs_review only by extracting doubtfully, and a permanent
 * link to an almost always empty list is worse than no link.
 */
export function primaryNavItems(reviewRequired: boolean): readonly NavItem[] {
  return [
    { kind: 'route', label: 'Documents', to: '/', exact: true },
    // A path rather than /?status=needs_review: a search-param link would be
    // active whenever Documents was, / being a subset of every search.
    ...(reviewRequired
      ? [{ kind: 'route', label: 'Inbox', to: '/inbox', badgeKey: 'inbox' } as const]
      : []),
    { kind: 'route', label: 'Upload', to: '/upload' },
    { kind: 'route', label: 'Activity', to: '/activity', badgeKey: 'activity' },
    { kind: 'route', label: 'Deep Research', to: '/rag/research' },
  ]
}

/** The links behind the bar's "More" menu, listed inline on a narrow one. */
export function secondaryNavItems(pbAdminUrl: string): readonly NavItem[] {
  return [
    { kind: 'route', label: 'Account', to: '/account' },
    { kind: 'route', label: 'Tags', to: '/tags' },
    { kind: 'route', label: 'OCR test', to: '/ocr-test' },
    { kind: 'route', label: 'Export', to: '/export' },
    { kind: 'route', label: 'Import', to: '/import' },
    { kind: 'route', label: 'Settings', to: '/settings', admin: true },
    { kind: 'route', label: 'Maintenance', to: '/maintenance', admin: true },
    { kind: 'external', label: 'Admin', href: pbAdminUrl, admin: true },
  ]
}

/** The admin routes bounce non-admins anyway; this avoids offering a dead end. */
export function visibleNavItems(items: readonly NavItem[], admin: boolean): NavItem[] {
  return items.filter((item) => !item.admin || admin)
}
