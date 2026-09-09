import { describe, expect, test } from 'vitest'
import {
  NAV_BADGE_DESCRIPTIONS,
  primaryNavItems,
  secondaryNavItems,
  visibleNavItems,
} from './nav'

describe('nav items', () => {
  test('hides admin-only entries from a regular user', () => {
    const items = visibleNavItems(secondaryNavItems('http://pb.test/_/'), false)
    const labels = items.map((item) => item.label)

    expect(labels).toEqual(['Account', 'OCR test', 'Export', 'Import'])
    expect(items.every((item) => !item.admin)).toBe(true)
  })

  test('gives an admin the full secondary set', () => {
    const items = visibleNavItems(secondaryNavItems('http://pb.test/_/'), true)

    expect(items.map((item) => item.label)).toEqual([
      'Account',
      'OCR test',
      'Export',
      'Import',
      'Settings',
      'Management',
      'Admin',
    ])
  })

  test('points the admin entry at the PocketBase dashboard', () => {
    const admin = secondaryNavItems('http://pb.test/_/').find((item) => item.label === 'Admin')

    expect(admin).toEqual({ kind: 'external', label: 'Admin', href: 'http://pb.test/_/', admin: true })
  })

  test('keeps the primary links open to everyone', () => {
    const items = primaryNavItems(true)
    expect(visibleNavItems(items, false)).toHaveLength(items.length)
    expect(items.map((item) => item.label)).toEqual([
      'Documents',
      'Inbox',
      'Upload',
      'Activity',
      'Deep Search',
    ])
  })

  // Without the setting, needs_review is only ever reached by a doubtful
  // extraction, so a permanent Inbox link would point at an empty list.
  test('offers the Inbox only where review is required', () => {
    expect(primaryNavItems(false).map((item) => item.label)).toEqual([
      'Documents',
      'Upload',
      'Activity',
      'Deep Search',
    ])
  })

  test('gives the Inbox and Activity paths of their own, and the count badges', () => {
    const badged = primaryNavItems(true).filter((item) => item.kind === 'route' && item.badgeKey)

    expect(badged).toEqual([
      { kind: 'route', label: 'Inbox', to: '/inbox', badgeKey: 'inbox' },
      { kind: 'route', label: 'Activity', to: '/activity', badgeKey: 'activity' },
    ])
  })

  // The header reads the digits out as "3 waiting for review", so a badge key
  // added without its sentence would render an undefined one.
  test('describes every badge key for assistive tech', () => {
    for (const item of primaryNavItems(true)) {
      if (item.kind === 'route' && item.badgeKey) {
        expect(NAV_BADGE_DESCRIPTIONS[item.badgeKey]).toBeTruthy()
      }
    }
  })

  // Only the document list is exact: every other link has children it should
  // stay lit for (/upload/split, /rag/research, /import/ngx).
  test('marks only the document list as an exact match', () => {
    const exact = [...primaryNavItems(true), ...secondaryNavItems('/_/')].filter(
      (item) => item.kind === 'route' && item.exact,
    )

    expect(exact.map((item) => item.label)).toEqual(['Documents'])
  })
})
