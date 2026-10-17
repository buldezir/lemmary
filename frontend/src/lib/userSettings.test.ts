import type { RecordModel } from 'pocketbase'
import { describe, expect, test } from 'vitest'
import { cardLayoutOf, languageOf } from './userSettings'

const user = (preferences: unknown) =>
  ({ id: 'u1', collectionId: 'c', collectionName: 'users', preferences }) as RecordModel

describe('cardLayoutOf', () => {
  test('reads the preview layout', () => {
    expect(cardLayoutOf(user({ card_layout: 'preview' }))).toBe('preview')
  })

  // No choice is every account that never chose, and the pre-setting behaviour.
  test('falls back to the summary for anything else', () => {
    expect(cardLayoutOf(user({ card_layout: 'summary' }))).toBe('summary')
    expect(cardLayoutOf(user({ card_layout: 'gallery' }))).toBe('summary')
    expect(cardLayoutOf(user({}))).toBe('summary')
    expect(cardLayoutOf(null)).toBe('summary')
  })
})

describe('languageOf', () => {
  test('reads a language the app ships', () => {
    expect(languageOf(user({ language: 'de' }))).toBe('de')
  })

  // No choice leaves this browser's cookie, or the browser itself, in charge.
  test('is no choice for unknown or prototype names', () => {
    expect(languageOf(user({ language: 'fr' }))).toBeNull()
    expect(languageOf(user({ language: 'constructor' }))).toBeNull()
    expect(languageOf(user({}))).toBeNull()
    expect(languageOf(null)).toBeNull()
  })
})

// The account writes the field itself, so it may hold anything JSON can.
test('a field that is not an object is no choice at all', () => {
  for (const stored of [null, '', 'preview', ['preview'], 7]) {
    expect(cardLayoutOf(user(stored))).toBe('summary')
    expect(languageOf(user(stored))).toBeNull()
  }
})
