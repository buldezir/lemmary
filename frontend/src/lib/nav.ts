import type { LinkProps } from '@tanstack/react-router'

/**
 * The header's link set, as data.
 *
 * The bar and the mobile panel render the same items in two very different
 * shapes, and a link added to only one of them is the failure mode that keeps
 * happening -- so the list lives here once and both walk it.
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
 * The links that sit in the header bar itself on a wide viewport.
 *
 * A function, like secondaryNavItems, because the Inbox is conditional: it is
 * the landing place for a document that must be reviewed, so it is offered only
 * where the instance requires review of every document. Without that setting a
 * document reaches needs_review only by extracting doubtfully, which the status
 * filter on Documents already finds -- a permanent link to a list that is
 * almost always empty is a worse answer than no link.
 */
export function primaryNavItems(reviewRequired: boolean): readonly NavItem[] {
  return [
    { kind: 'route', label: 'Documents', to: '/', exact: true },
    // A path rather than /?status=needs_review: a search-param link would be
    // active whenever Documents was, since the empty search of / is a subset of
    // every search.
    ...(reviewRequired
      ? [{ kind: 'route', label: 'Inbox', to: '/inbox', badgeKey: 'inbox' } as const]
      : []),
    { kind: 'route', label: 'Upload', to: '/upload' },
    { kind: 'route', label: 'Activity', to: '/activity', badgeKey: 'activity' },
    // /rag, not a mode: it is the one path above both, so this marks itself
    // active in Search and Research alike.
    { kind: 'route', label: 'Deep Search', to: '/rag' },
  ]
}

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
