import { describe, expect, it } from 'vitest'
import { passkeyDateLabel } from './passkeys'

describe('passkeyDateLabel', () => {
  it('shortens a PocketBase timestamp to the date', () => {
    expect(passkeyDateLabel('2026-08-28 05:56:12.123Z')).toBe('2026-08-28')
  })

  it('reports a passkey that has never been used', () => {
    expect(passkeyDateLabel('')).toBe('Never')
    expect(passkeyDateLabel('   ')).toBe('Never')
  })

  it('leaves an already-short value alone', () => {
    expect(passkeyDateLabel('2026-08-28')).toBe('2026-08-28')
  })
})
