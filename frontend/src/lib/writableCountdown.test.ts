import { describe, expect, test } from 'vitest'
import { writableCountdown } from './writableCountdown'

describe('writableCountdown', () => {
  const until = Date.parse('2026-01-02T15:00:00Z')

  test('counts down, rounding up so it reaches 0:00 at the deadline', () => {
    expect(writableCountdown(until, until - 29 * 60_000 - 41_000).text).toBe('29:41 left')
    expect(writableCountdown(until, until - 500).text).toBe('0:01 left')
    expect(writableCountdown(until, until - 2 * 3600_000 - 5_000).text).toBe('2:00:05 left')
    expect(writableCountdown(until, until - 60_000).note).toBe('then read-only, deleted soon after')
  })

  test('says read-only once the deadline has passed', () => {
    expect(writableCountdown(until, until)).toEqual({ text: 'Read-only', note: 'deleted soon' })
    expect(writableCountdown(until, until + 60_000).text).toBe('Read-only')
  })
})
