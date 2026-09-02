import { describe, expect, test } from 'vitest'
import { primaryNavItems, secondaryNavItems, visibleNavItems } from './nav'

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
    expect(visibleNavItems(primaryNavItems, false)).toHaveLength(primaryNavItems.length)
    expect(primaryNavItems.map((item) => item.label)).toEqual([
      'Documents',
      'Upload',
      'Deep Search',
    ])
  })

  // Only the document list is exact: every other link has children it should
  // stay lit for (/upload/split, /rag/research, /import/ngx).
  test('marks only the document list as an exact match', () => {
    const exact = [...primaryNavItems, ...secondaryNavItems('/_/')].filter(
      (item) => item.kind === 'route' && item.exact,
    )

    expect(exact.map((item) => item.label)).toEqual(['Documents'])
  })
})
