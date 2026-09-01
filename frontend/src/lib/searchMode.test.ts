import { afterEach, describe, expect, test, vi } from 'vitest'
import { readPreferredSearchMode, writePreferredSearchMode } from './searchMode'

function stubStorage(store: Partial<Storage>) {
  vi.stubGlobal('localStorage', store)
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('preferred search mode', () => {
  test('round-trips a written mode', () => {
    const values = new Map<string, string>()
    stubStorage({
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => void values.set(key, value),
    })

    writePreferredSearchMode('research')
    expect(readPreferredSearchMode()).toBe('research')
    writePreferredSearchMode('search')
    expect(readPreferredSearchMode()).toBe('search')
  })

  test('falls back to search when nothing is stored', () => {
    stubStorage({ getItem: () => null, setItem: () => {} })
    expect(readPreferredSearchMode()).toBe('search')
  })

  // The key is a plain string in someone else's browser: a stale value from an
  // older build, or one edited by hand, must not become the mode.
  test('rejects a stored value that is not a mode', () => {
    for (const stored of ['deep', 'shallow', '', 'Research', 'null']) {
      stubStorage({ getItem: () => stored, setItem: () => {} })
      expect(readPreferredSearchMode()).toBe('search')
    }
  })

  // Private mode and blocked site data throw on access rather than returning
  // null. A remembered toggle position is not worth a broken page.
  test('survives a browser that refuses storage', () => {
    stubStorage({
      getItem: () => {
        throw new Error('denied')
      },
      setItem: () => {
        throw new Error('denied')
      },
    })
    expect(readPreferredSearchMode()).toBe('search')
    expect(() => writePreferredSearchMode('research')).not.toThrow()
  })
})
