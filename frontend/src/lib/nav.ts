import type { LinkProps } from '@tanstack/react-router'
import { t } from '../i18n'

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
  inbox: t('nav.badgeInbox'),
  activity: t('nav.badgeActivity'),
}

/** Names an icon the header draws before a label -- the key, never the SVG. */
export type NavIconKey = 'pocketbase'

type NavItemBase = {
  label: string
  /** Rendered for admins only, and labelled with a shield. */
  admin?: boolean
  icon?: NavIconKey
}

export type RouteNavItem = NavItemBase & {
  kind: 'route'
  to: LinkProps['to']
  /** Marks the link active only on an exact path match. */
  exact?: boolean
  /** Which count to show beside the label, if any. */
  badgeKey?: NavBadgeKey
}

export type ExternalNavItem = NavItemBase & {
  kind: 'external'
  href: string
}

export type NavItem = RouteNavItem | ExternalNavItem

/**
 * A function because the Inbox is conditional: without always_require_review a
 * document reaches needs_review only by extracting doubtfully, and a permanent
 * link to an almost always empty list is worse than no link.
 */
export function primaryNavItems(reviewRequired: boolean): readonly NavItem[] {
  return [
    { kind: 'route', label: t('nav.documents'), to: '/', exact: true },
    // A path rather than /?status=needs_review: a search-param link would be
    // active whenever Documents was, / being a subset of every search.
    ...(reviewRequired
      ? [{ kind: 'route', label: t('nav.inbox'), to: '/inbox', badgeKey: 'inbox' } as const]
      : []),
    { kind: 'route', label: t('nav.upload'), to: '/upload' },
    { kind: 'route', label: t('nav.activity'), to: '/activity', badgeKey: 'activity' },
    { kind: 'route', label: t('nav.deepResearch'), to: '/rag/research' },
  ]
}

/** The links behind the bar's "More" menu, listed inline on a narrow one. */
export function secondaryNavItems(pbAdminUrl: string): readonly NavItem[] {
  return [
    { kind: 'route', label: t('nav.account'), to: '/account' },
    { kind: 'route', label: t('nav.tags'), to: '/tags' },
    { kind: 'route', label: t('nav.bulkActions'), to: '/bulk' },
    { kind: 'route', label: t('nav.ocrTest'), to: '/ocr-test' },
    { kind: 'route', label: t('nav.export'), to: '/export' },
    { kind: 'route', label: t('nav.import'), to: '/import' },
    { kind: 'route', label: t('nav.settings'), to: '/settings', admin: true },
    { kind: 'route', label: t('nav.management'), to: '/management', admin: true },
    { kind: 'route', label: t('nav.maintenance'), to: '/maintenance', admin: true },
    { kind: 'external', label: t('nav.admin'), href: pbAdminUrl, admin: true, icon: 'pocketbase' },
  ]
}

/** The admin routes bounce non-admins anyway; this avoids offering a dead end. */
export function visibleNavItems(items: readonly NavItem[], admin: boolean): NavItem[] {
  return items.filter((item) => !item.admin || admin)
}
