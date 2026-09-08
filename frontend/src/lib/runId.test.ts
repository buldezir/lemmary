import { describe, expect, it } from 'vitest'
import { runId } from './runId'

describe('runId', () => {
  // The bug this replaced: crypto.randomUUID is secure-context only, so Deep
  // Search threw "crypto.randomUUID is not a function" on an instance reached
  // over plain HTTP at a LAN address. Nothing here may touch crypto.
  it('needs no Web Crypto', () => {
    const real = globalThis.crypto
    Object.defineProperty(globalThis, 'crypto', { value: undefined, configurable: true })
    try {
      expect(() => runId()).not.toThrow()
      expect(runId()).toMatch(/^run-/)
    } finally {
      Object.defineProperty(globalThis, 'crypto', { value: real, configurable: true })
    }
  })

  it('is unique across calls', () => {
    const ids = new Set(Array.from({ length: 1000 }, () => runId()))
    expect(ids.size).toBe(1000)
  })

  it('is a non-empty string safe to put in a JSON body', () => {
    const id = runId()
    expect(id.length).toBeGreaterThan(8)
    expect(id).toBe(id.trim())
    // The server trims and compares it verbatim, so nothing exotic.
    expect(id).toMatch(/^[a-z0-9-]+$/)
  })
})
