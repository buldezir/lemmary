import { describe, expect, it } from 'vitest'

import {
  boundedLimits,
  formatBytes,
  isExhausted,
  limitFromError,
  type InstanceLimits,
} from './limits'

const unlimited = { used: 0, unlimited: true }

function limits(overrides: Partial<InstanceLimits> = {}): InstanceLimits {
  return {
    enforced: true,
    documents: unlimited,
    document_pages: unlimited,
    storage_bytes: unlimited,
    file_bytes: unlimited,
    file_pages: unlimited,
    additional_users: unlimited,
    ...overrides,
  }
}

describe('limitFromError', () => {
  // The exact shape the backend puts on the wire, verified against a running
  // instance: PocketBase nests a SafeErrorItem under code/params.
  it('reads the limit name out of params', () => {
    const err = {
      response: {
        data: {
          limit: {
            code: 'limit_documents',
            message: 'This instance holds 2 of 2 documents, so there is no room for another.',
            params: { limit: 'documents', allowed: 2, used: 2 },
          },
        },
      },
    }
    expect(limitFromError(err)).toBe('documents')
  })

  it('falls back to the code when params are absent', () => {
    expect(limitFromError({ response: { data: { limit: { code: 'limit_storage_bytes' } } } })).toBe(
      'storage_bytes',
    )
  })

  it('returns null for an unrelated failure', () => {
    expect(limitFromError(null)).toBeNull()
    expect(limitFromError(new Error('network'))).toBeNull()
    // A duplicate rejection uses the same envelope but a different key.
    expect(limitFromError({ response: { data: { duplicate_of: 'abc' } } })).toBeNull()
  })

  // A plain string is what PocketBase would have mangled into
  // "validation_invalid_value", so treat it as absent rather than trusting it.
  it('ignores a limit name it does not recognise', () => {
    expect(limitFromError({ response: { data: { limit: { code: 'limit_nonsense' } } } })).toBeNull()
    expect(
      limitFromError({
        response: { data: { limit: { code: 'validation_invalid_value', message: 'Invalid value.' } } },
      }),
    ).toBeNull()
  })
})

describe('formatBytes', () => {
  // Matches the backend's own formatting, so a quota bar and the rejection
  // message that follows it never disagree.
  it.each([
    [0, '0 B'],
    [512, '512 B'],
    [1024, '1.0 KB'],
    [20 * 1024 * 1024, '20 MB'],
    [1024 ** 3, '1.0 GB'],
  ])('renders %i as %s', (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want)
  })

  it('survives a missing or nonsense value', () => {
    expect(formatBytes(-1)).toBe('0 B')
    expect(formatBytes(Number.NaN)).toBe('0 B')
  })
})

describe('isExhausted', () => {
  it('is true only when a bounded limit is fully used', () => {
    expect(isExhausted({ used: 2, limit: 3, unlimited: false })).toBe(false)
    expect(isExhausted({ used: 3, limit: 3, unlimited: false })).toBe(true)
    // A limit lowered under an existing library reads as over, not negative.
    expect(isExhausted({ used: 9, limit: 3, unlimited: false })).toBe(true)
    expect(isExhausted(unlimited)).toBe(false)
  })
})

describe('boundedLimits', () => {
  it('drops the unlimited ones rather than showing an infinity sign', () => {
    const rows = boundedLimits(limits({ documents: { used: 1, limit: 5, unlimited: false } }))
    expect(rows.map((row) => row.name)).toEqual(['documents'])
  })

  it('is empty when nothing is bounded, so the UI renders nothing', () => {
    expect(boundedLimits(limits())).toEqual([])
  })

  it('formats storage as bytes and the rest as counts', () => {
    const rows = boundedLimits(
      limits({
        documents: { used: 1200, limit: 5000, unlimited: false },
        storage_bytes: { used: 1024, limit: 2048, unlimited: false },
      }),
    )
    const byName = Object.fromEntries(rows.map((row) => [row.name, row]))
    expect(byName.storage_bytes.format(1024)).toBe('1.0 KB')
    expect(byName.documents.format(1200)).toBe((1200).toLocaleString())
  })

  it('keeps a zero allowance, which is a real plan', () => {
    const rows = boundedLimits(limits({ additional_users: { used: 0, limit: 0, unlimited: false } }))
    expect(rows.map((row) => row.name)).toEqual(['additional_users'])
  })
})
