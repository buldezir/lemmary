import { describe, expect, test } from 'vitest'
import {
  DOCUMENT_STATUSES,
  DOCUMENT_STATUS_LABELS,
  isDocumentStatus,
  reviewReason,
} from './documentStatus'

describe('document statuses', () => {
  test('labels every status', () => {
    for (const status of DOCUMENT_STATUSES) {
      expect(DOCUMENT_STATUS_LABELS[status]).toBeTruthy()
    }
    expect(Object.keys(DOCUMENT_STATUS_LABELS)).toHaveLength(DOCUMENT_STATUSES.length)
  })

  test('matches the values the database will accept', () => {
    expect([...DOCUMENT_STATUSES]).toEqual([
      'pending',
      'processing',
      'completed',
      'failed',
      'needs_review',
    ])
  })

  test('accepts every status and rejects anything else', () => {
    for (const status of DOCUMENT_STATUSES) {
      expect(isDocumentStatus(status)).toBe(true)
    }
    for (const junk of ['all', 'running', '', 'NEEDS_REVIEW', 3, null, undefined, {}]) {
      expect(isDocumentStatus(junk)).toBe(false)
    }
  })
})

describe('reviewReason', () => {
  // The duplicate wins even when confidence was also low: it is the reason
  // with another document to go and look at.
  test('names the duplicate ahead of the confidence', () => {
    expect(reviewReason({ duplicate_of: 'abc', confidence: 0.1 })).toBe('duplicate')
    expect(reviewReason({ duplicate_of: 'abc', confidence: 0.9 })).toBe('duplicate')
  })

  test('names low confidence only below the threshold', () => {
    expect(reviewReason({ confidence: 0.49 })).toBe('low_confidence')
    expect(reviewReason({ confidence: 0.5 })).toBe('awaiting')
    expect(reviewReason({ confidence: 0.9 })).toBe('awaiting')
  })

  // The case the setting creates, and the one the old code got wrong.
  test('says a confident document is merely awaiting review', () => {
    expect(reviewReason({ confidence: 0.98 })).toBe('awaiting')
  })

  // No confidence recorded is not a low one: extraction may never have run.
  test('treats a missing confidence as awaiting rather than low', () => {
    expect(reviewReason({})).toBe('awaiting')
    expect(reviewReason({ confidence: 0 })).toBe('awaiting')
  })
})
