import { describe, expect, it } from 'vitest'

import { describeScan, recallScanner, rememberScanner } from './scan'

describe('describeScan', () => {
  it('counts one page without pluralising it', () => {
    expect(describeScan({ upload_id: 'x', page_count: 1, size_bytes: 240_000, expires_at: '' })).toBe(
      '1 page · 0.2 MB',
    )
  })

  it('falls back to kilobytes for a page that barely weighs anything', () => {
    expect(describeScan({ upload_id: 'x', page_count: 3, size_bytes: 40_000, expires_at: '' })).toBe(
      '3 pages · 39 KB',
    )
  })
})

describe('the remembered scanner', () => {
  // Runs without a DOM, which is also what a browser with site data blocked
  // looks like: the picker has to come up empty rather than throw.
  it('survives a browser with no storage at all', () => {
    expect(() => rememberScanner('http://192.168.1.9/eSCL')).not.toThrow()
    expect(recallScanner()).toBe('')
  })
})
